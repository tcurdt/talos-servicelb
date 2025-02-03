package main

import (
	"fmt"
	"os/exec"
	"strings"
)

type NftController struct {
	path string
}

func (c *NftController) setup() error {
	cmds := [][]string{
		{"add", "table", "ip", "nat"},
		{"add", "chain", "ip", "nat", "prerouting", "{ type nat hook prerouting priority 0; }"},
		{"add", "chain", "ip", "nat", "postrouting", "{ type nat hook postrouting priority 100; }"},
	}

	for _, cmd := range cmds {
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
			if !strings.Contains(string(out), "File exists") {
				return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
			}
		}
	}
	return nil
}

func (c *NftController) addPort(nodeIP string, servicePort, nodePort int32) error {
	cmds := [][]string{
		{
			"add", "rule", "ip", "nat", "prerouting",
			"ip", "daddr", nodeIP,
			"tcp", "dport", fmt.Sprintf("%d", servicePort),
			"dnat", "to", fmt.Sprintf(":%d", nodePort),
		},
		{
			"add", "rule", "ip", "nat", "postrouting",
			"ip", "daddr", "10.0.0.0/8",
			"tcp", "dport", fmt.Sprintf("%d", nodePort),
			"masquerade",
		},
	}

	for _, cmd := range cmds {
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
		}
	}
	return nil
}

func (c *NftController) removePort(nodeIP string, servicePort, nodePort int32) error {
	cmds := [][]string{
		{
			"delete", "rule", "ip", "nat", "prerouting",
			"ip", "daddr", nodeIP,
			"tcp", "dport", fmt.Sprintf("%d", servicePort),
		},
		{
			"delete", "rule", "ip", "nat", "postrouting",
			"ip", "daddr", "10.0.0.0/8",
			"tcp", "dport", fmt.Sprintf("%d", nodePort),
		},
	}

	for _, cmd := range cmds {
		if out, err := exec.Command(c.path, cmd...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s %v failed: %v, output: %s", c.path, cmd, err, out)
		}
	}
	return nil
}
