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

type Controller struct {
	clientset       *kubernetes.Clientset
	serviceInformer cache.SharedIndexInformer
	nodeIP          string
}

func NewController(clientset *kubernetes.Clientset) (*Controller, error) {
	// get primary interface IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get interfaces: %v", err)
	}

	var nodeIP string
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
					nodeIP = ipnet.IP.String()
					break
				}
			}
		}
		if nodeIP != "" {
			break
		}
	}
	if nodeIP == "" {
		return nil, fmt.Errorf("could not determine node IP")
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
	// update service status with node IP
	service.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{
		{
			IP: c.nodeIP,
		},
	}

	_, err := c.clientset.CoreV1().Services(service.Namespace).UpdateStatus(context.Background(), service, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Error updating service status: %v", err)
		return
	}

	// setup nftables rules for each port
	for _, port := range service.Spec.Ports {
		cmd := exec.Command("nft", "add", "rule", "ip", "filter", "forward",
			fmt.Sprintf("ip daddr %s tcp dport %d accept", c.nodeIP, port.Port))
		if err := cmd.Run(); err != nil {
			log.Printf("Error setting up nftables rule: %v", err)
		}
	}
}

func (c *Controller) cleanupLoadBalancer(service *corev1.Service) {
	// remove nftables rules for each port
	for _, port := range service.Spec.Ports {
		cmd := exec.Command("nft", "delete", "rule", "ip", "filter", "forward",
			fmt.Sprintf("ip daddr %s tcp dport %d accept", c.nodeIP, port.Port))
		if err := cmd.Run(); err != nil {
			log.Printf("Error removing nftables rule: %v", err)
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
