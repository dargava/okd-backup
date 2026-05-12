package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// NodeInfo holds the name and addresses of a control plane node.
type NodeInfo struct {
	Name      string
	Addresses []string
}

// listControlPlaneNodes queries the cluster for nodes with the
// control-plane or master role and returns their addresses.
func listControlPlaneNodes() ([]NodeInfo, error) {
	// Try control-plane label first, fall back to master
	labels := []string{
		"node-role.kubernetes.io/control-plane",
		"node-role.kubernetes.io/master",
	}

	for _, label := range labels {
		rc, out, _ := runOc([]string{
			"get", "nodes", "-l", label,
			"-o", "json",
		}, "")
		if rc != 0 || out == "" {
			continue
		}

		var nodeList struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Addresses []struct {
						Type    string `json:"type"`
						Address string `json:"address"`
					} `json:"addresses"`
				} `json:"status"`
			} `json:"items"`
		}

		if err := json.Unmarshal([]byte(out), &nodeList); err != nil {
			continue
		}

		if len(nodeList.Items) == 0 {
			continue
		}

		var nodes []NodeInfo
		for _, item := range nodeList.Items {
			node := NodeInfo{Name: item.Metadata.Name}
			// Priority order: InternalIP, ExternalIP, Hostname
			addrByType := map[string]string{}
			for _, addr := range item.Status.Addresses {
				addrByType[addr.Type] = addr.Address
			}
			for _, t := range []string{"InternalIP", "ExternalIP", "Hostname"} {
				if v, ok := addrByType[t]; ok {
					node.Addresses = append(node.Addresses, v)
				}
			}
			nodes = append(nodes, node)
		}
		return nodes, nil
	}

	return nil, nil
}

// isReachable does a TCP connect test to host:port with the given timeout.
func isReachable(host string, port, timeout int) bool {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, time.Duration(timeout)*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// detectControlPlane finds the first reachable control plane node via SSH TCP probe.
func detectControlPlane(sshPort, timeout int) string {
	nodes, err := listControlPlaneNodes()
	if err != nil || len(nodes) == 0 {
		return ""
	}

	for _, node := range nodes {
		for _, addr := range node.Addresses {
			if isReachable(addr, sshPort, timeout) {
				return addr
			}
		}
	}
	return ""
}

// listNamespaces returns all namespace names from the cluster.
func listNamespaces() []string {
	rc, out, _ := runOc([]string{
		"get", "namespaces",
		"-o", "jsonpath={.items[*].metadata.name}",
	}, "")
	if rc != 0 || out == "" {
		return nil
	}
	return strings.Fields(out)
}
