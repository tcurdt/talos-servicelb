package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

type IptablesController struct {
	path string
}

func (c *IptablesController) setup() error {

	{
		args := []string{"-V"}
		log.Printf("cmd [%s %s]", c.path, strings.Join(args, " "))
		cmd := exec.Command(c.path, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ERR: cmd [%s -V] => [%s], err: %v", c.path, string(out), err)
		}
		log.Printf("OK: cmd [%s %s] => [%s]", c.path, strings.Join(args, " "), string(out))
	}

	{
		args := []string{"-t", "nat", "-L"}
		log.Printf("cmd [%s %s]", c.path, strings.Join(args, " "))
		cmd := exec.Command(c.path, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ERR: cmd [%s -V] => [%s], err: %v", c.path, string(out), err)
		}
		log.Printf("OK: cmd [%s %s] => [%s]", c.path, strings.Join(args, " "), string(out))
	}

	cmds := [][]string{
		{"-t", "nat", "-N", "LB-PREROUTING"},
		{"-t", "nat", "-A", "PREROUTING", "-j", "LB-PREROUTING"},
		{"-t", "nat", "-N", "LB-POSTROUTING"},
		{"-t", "nat", "-A", "POSTROUTING", "-j", "LB-POSTROUTING"},
	}

	for _, args := range cmds {
		log.Printf("cmd [%s %s]", c.path, strings.Join(args, " "))
		command := exec.Command(c.path, args...)
		out, err := command.CombinedOutput()
		if err != nil {
			if !strings.Contains(string(out), "already exists") {
				return fmt.Errorf("ERR: cmd [%s %s] => [%s], err: %v", c.path, strings.Join(args, " "), string(out), err)
			}
			log.Printf("OK: chains already exist [%s]", string(out))
			continue
		}
		log.Printf("OK: cmd [%s %s] => [%s]", c.path, strings.Join(args, " "), string(out))
	}
	return nil
}

func (c *IptablesController) addPort(nodeIP string, servicePort, nodePort int32) error {
	log.Printf("addPort [%s] servicePort=%d nodePort=%d", nodeIP, servicePort, nodePort)

	cmds := [][]string{
		{
			"-t", "nat", "-A", "LB-PREROUTING",
			"-d", nodeIP, "-p", "tcp",
			"--dport", fmt.Sprintf("%d", servicePort),
			"-j", "DNAT", "--to-destination", fmt.Sprintf(":%d", nodePort),
		},
		{
			"-t", "nat", "-A", "LB-POSTROUTING",
			"-d", "10.0.0.0/8", "-p", "tcp",
			"--dport", fmt.Sprintf("%d", nodePort),
			"-j", "MASQUERADE",
		},
	}

	for _, args := range cmds {
		log.Printf("cmd [%s %s]", c.path, strings.Join(args, " "))
		command := exec.Command(c.path, args...)
		out, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ERR: cmd [%s %s] => [%s], err: %v", c.path, strings.Join(args, " "), string(out), err)
		}
		log.Printf("OK: cmd [%s %s] => [%s]", c.path, strings.Join(args, " "), string(out))
	}
	return nil
}

func (c *IptablesController) removePort(nodeIP string, servicePort, nodePort int32) error {
	log.Printf("removePort [%s] servicePort=%d nodePort=%d", nodeIP, servicePort, nodePort)

	cmds := [][]string{
		{
			"-t", "nat", "-D", "LB-PREROUTING",
			"-d", nodeIP, "-p", "tcp",
			"--dport", fmt.Sprintf("%d", servicePort),
			"-j", "DNAT", "--to-destination", fmt.Sprintf(":%d", nodePort),
		},
		{
			"-t", "nat", "-D", "LB-POSTROUTING",
			"-d", "10.0.0.0/8", "-p", "tcp",
			"--dport", fmt.Sprintf("%d", nodePort),
			"-j", "MASQUERADE",
		},
	}

	for _, args := range cmds {
		log.Printf("cmd [%s %s]", c.path, strings.Join(args, " "))
		out, err := exec.Command(c.path, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("ERR: cmd [%s %s] => [%s], err: %v", c.path, strings.Join(args, " "), string(out), err)
		}
		log.Printf("OK: cmd [%s %s] => [%s]", c.path, strings.Join(args, " "), string(out))
	}
	return nil
}
