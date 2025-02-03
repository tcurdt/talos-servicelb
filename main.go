package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type Controller struct {
	clientset       *kubernetes.Clientset
	serviceInformer cache.SharedIndexInformer
	nodeIP          string
}

func getPublicIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("failed to get interfaces: %v", err)
	}

	for _, iface := range ifaces {
		if (iface.Flags & net.FlagUp) == 0 {
			continue
		}
		if (iface.Flags & net.FlagLoopback) != 0 {
			continue
		}
		if iface.Name != "enp1s0" {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no public IP found on enp1s0")
}

func NewController(clientset *kubernetes.Clientset) (*Controller, error) {
	nodeIP, err := getPublicIP()
	if err != nil {
		return nil, err
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	serviceInformer := factory.Core().V1().Services().Informer()

	c := &Controller{
		clientset:       clientset,
		serviceInformer: serviceInformer,
		nodeIP:          nodeIP,
	}

	serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleAdd,
		UpdateFunc: c.handleUpdate,
		DeleteFunc: c.handleDelete,
	})

	return c, nil
}

func (c *Controller) setupNftables() error {
	// create table if it doesn't exist
	cmds := [][]string{
		{"add", "table", "ip", "nat"},
		{"add", "chain", "ip", "nat", "prerouting", "{ type nat hook prerouting priority 0; }"},
		{"add", "chain", "ip", "nat", "postrouting", "{ type nat hook postrouting priority 100; }"},
	}

	for _, cmd := range cmds {
		out, err := exec.Command("nft", cmd...).CombinedOutput()
		if err != nil && !strings.Contains(string(out), "File exists") {
			return fmt.Errorf("nft %v failed: %v, output: %s", cmd, err, out)
		}
	}
	return nil
}

func (c *Controller) handleAdd(obj interface{}) {
	service := obj.(*corev1.Service)
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		c.setupLoadBalancer(service)
	}
}

func (c *Controller) handleUpdate(old, new interface{}) {
	oldService := old.(*corev1.Service)
	newService := new.(*corev1.Service)

	if oldService.Spec.Type != corev1.ServiceTypeLoadBalancer &&
		newService.Spec.Type == corev1.ServiceTypeLoadBalancer {
		c.setupLoadBalancer(newService)
	} else if oldService.Spec.Type == corev1.ServiceTypeLoadBalancer &&
		newService.Spec.Type != corev1.ServiceTypeLoadBalancer {
		c.cleanupLoadBalancer(newService)
	}
}

func (c *Controller) handleDelete(obj interface{}) {
	service := obj.(*corev1.Service)
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		c.cleanupLoadBalancer(service)
	}
}

func (c *Controller) setupLoadBalancer(service *corev1.Service) {
	if err := c.setupNftables(); err != nil {
		log.Printf("Error setting up nftables: %v", err)
		return
	}

	// get existing ingress IPs
	ingressIPs := make(map[string]bool)
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		ingressIPs[ingress.IP] = true
	}

	// add IP if not present
	if !ingressIPs[c.nodeIP] {
		service.Status.LoadBalancer.Ingress = append(
			service.Status.LoadBalancer.Ingress,
			corev1.LoadBalancerIngress{IP: c.nodeIP},
		)
	}

	_, err := c.clientset.CoreV1().Services(service.Namespace).UpdateStatus(context.Background(), service, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Error updating service status: %v", err)
		return
	}

	for _, port := range service.Spec.Ports {
		targetPort := port.NodePort
		if targetPort == 0 {
			continue
		}

		// add DNAT rule
		dnatCmd := []string{
			"add", "rule", "ip", "nat", "prerouting",
			"ip", "daddr", c.nodeIP,
			"tcp", "dport", fmt.Sprintf("%d", port.Port),
			"dnat", "to", fmt.Sprintf(":%d", targetPort),
		}
		if out, err := exec.Command("nft", dnatCmd...).CombinedOutput(); err != nil {
			log.Printf("Error setting up DNAT rule: %v, output: %s", err, out)
		}

		// add masquerade rule
		masqCmd := []string{
			"add", "rule", "ip", "nat", "postrouting",
			"ip", "daddr", "10.0.0.0/8",
			"tcp", "dport", fmt.Sprintf("%d", targetPort),
			"masquerade",
		}
		if out, err := exec.Command("nft", masqCmd...).CombinedOutput(); err != nil {
			log.Printf("Error setting up masquerade rule: %v, output: %s", err, out)
		}
	}
}

func (c *Controller) cleanupLoadBalancer(service *corev1.Service) {
	for _, port := range service.Spec.Ports {
		targetPort := port.NodePort
		if targetPort == 0 {
			continue
		}

		dnatCmd := []string{
			"delete", "rule", "ip", "nat", "prerouting",
			"ip", "daddr", c.nodeIP,
			"tcp", "dport", fmt.Sprintf("%d", port.Port),
		}
		if out, err := exec.Command("nft", dnatCmd...).CombinedOutput(); err != nil {
			log.Printf("Error removing DNAT rule: %v, output: %s", err, out)
		}

		masqCmd := []string{
			"delete", "rule", "ip", "nat", "postrouting",
			"ip", "daddr", "10.0.0.0/8",
			"tcp", "dport", fmt.Sprintf("%d", targetPort),
		}
		if out, err := exec.Command("nft", masqCmd...).CombinedOutput(); err != nil {
			log.Printf("Error removing masquerade rule: %v, output: %s", err, out)
		}
	}
}

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Error getting cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating clientset: %v", err)
	}

	controller, err := NewController(clientset)
	if err != nil {
		log.Fatalf("Error creating controller: %v", err)
	}

	stopCh := make(chan struct{})
	go controller.serviceInformer.Run(stopCh)

	select {}
}
