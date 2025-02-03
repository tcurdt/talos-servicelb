package main

import (
	"fmt"
	"os/exec"
	"strings"
)

type IptablesController struct {
	path string
}

func (c *IptablesController) setup() error {
	cmds := [][]string{
		{"-t", "nat", "-N", "LB-PREROUTING"},
		{"-t", "nat", "-A", "PREROUTING", "-j", "LB-PREROUTING"},
		{"-t", "nat", "-N", "LB-POSTROUTING"},
		{"-t", "nat", "-A", "POSTROUTING", "-j", "LB-POSTROUTING"},
	}

	for _, cmd := range cmds {
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
			if !strings.Contains(string(out), "already exists") {
				return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
			}
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
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
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
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
		}
	}
	return nil
}
