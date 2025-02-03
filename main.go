package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type FirewallController interface {
	setup() error
	addPort(nodeIP string, servicePort, nodePort int32) error
	removePort(nodeIP string, servicePort, nodePort int32) error
}

type Controller struct {
	clientset       *kubernetes.Clientset
	serviceInformer cache.SharedIndexInformer
	nodeIP          string
	fw              FirewallController
}

func getPublicIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("failed to get interfaces: %v", err)
	}

	// let's just print what we found
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.To4() != nil {
					log.Printf("Found IF:[%v] IP:[%v]", iface.Name, ipnet.IP.String())
				}
			}
		}
	}

	for _, iface := range ifaces {
		if (iface.Flags & net.FlagUp) == 0 {
			continue
		}
		if (iface.Flags & net.FlagLoopback) != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.To4() != nil {
					log.Printf("Picking IF:[%v] IP[%v]", iface.Name, ipnet.IP.String())
					return ipnet.IP.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no public IP found")
}

func findExecutable(names []string, paths []string) (string, error) {

	// try common locations
	for _, basePath := range paths {
		for _, name := range names {
			path := basePath + "/" + name
			_, err := exec.LookPath(path)
			log.Printf("LookPath [%s]: %v (err: %v)", path, path, err)
			if err == nil {
				return path, nil
			}
		}
	}

	// try PATH
	for _, name := range names {
		path, err := exec.LookPath(name)
		log.Printf("LookPath [%s]: %v (err: %v)", name, path, err)
		if err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("could not find any of %v in PATH or common locations", names)
}

func getFirewallController() (FirewallController, error) {
	commonPaths := []string{
		"/usr/sbin",
		"/sbin",
		"/usr/local/sbin",
		"/usr/local/bin",
	}

	if nftPath, err := findExecutable([]string{"nft"}, commonPaths); err == nil {
		return &NftController{path: nftPath}, nil
	}
	if iptablesPath, err := findExecutable([]string{"iptables-nft"}, commonPaths); err == nil {
		return &IptablesController{path: iptablesPath}, nil
	}
	return nil, fmt.Errorf("neither nft nor iptables-nft found in PATH or common locations")
}

func NewController(clientset *kubernetes.Clientset) (*Controller, error) {
	nodeIP, err := getPublicIP()
	if err != nil {
		return nil, err
	}

	fw, err := getFirewallController()
	if err != nil {
		return nil, err
	}

	if err := fw.setup(); err != nil {
		return nil, err
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	serviceInformer := factory.Core().V1().Services().Informer()

	c := &Controller{
		clientset:       clientset,
		serviceInformer: serviceInformer,
		nodeIP:          nodeIP,
		fw:              fw,
	}

	serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleAdd,
		UpdateFunc: c.handleUpdate,
		DeleteFunc: c.handleDelete,
	})

	return c, nil
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

	// existing ingress IPs
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

	_, err := c.clientset.CoreV1().Services(service.Namespace).UpdateStatus(
		context.Background(), service, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Error updating service status: %v", err)
		return
	}

	for _, port := range service.Spec.Ports {
		targetPort := port.NodePort
		if targetPort == 0 {
			continue
		}
		if err := c.fw.addPort(c.nodeIP, port.Port, targetPort); err != nil {
			log.Printf("Error setting up port forwarding: %v", err)
		}
	}
}

func (c *Controller) cleanupLoadBalancer(service *corev1.Service) {
	for _, port := range service.Spec.Ports {
		targetPort := port.NodePort
		if targetPort == 0 {
			continue
		}
		if err := c.fw.removePort(c.nodeIP, port.Port, targetPort); err != nil {
			log.Printf("Error removing port forwarding: %v", err)
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
