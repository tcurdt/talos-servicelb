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

	log.Printf("Testing iptables binary at %s", c.path)
	testCmd := exec.Command(c.path, "-V")
	if out, err := testCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to execute %s: %v (output: %s)", c.path, err, string(out))
	}

	cmds := [][]string{
		{"-t", "nat", "-N", "LB-PREROUTING"},
		{"-t", "nat", "-A", "PREROUTING", "-j", "LB-PREROUTING"},
		{"-t", "nat", "-N", "LB-POSTROUTING"},
		{"-t", "nat", "-A", "POSTROUTING", "-j", "LB-POSTROUTING"},
	}

	for _, cmd := range cmds {
		log.Printf("Executing: %s %v", c.path, cmd)
		command := exec.Command(c.path, cmd...)
		if out, err := command.CombinedOutput(); err != nil {
			if !strings.Contains(string(out), "already exists") {
				return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
			}
			log.Printf("Chain already exists (this is OK): %s", string(out))
		}
	}
	return nil
}

func (c *IptablesController) addPort(nodeIP string, servicePort, nodePort int32) error {
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

	for _, cmd := range cmds {
		log.Printf("Executing: %s %v", c.path, cmd)
		command := exec.Command(c.path, cmd...)
		if out, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
		}
	}
	return nil
}

func (c *IptablesController) removePort(nodeIP string, servicePort, nodePort int32) error {
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

	for _, cmd := range cmds {
		log.Printf("Executing: %s %v", c.path, cmd)
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
		}
	}
	return nil
}
