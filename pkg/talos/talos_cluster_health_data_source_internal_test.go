// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package talos

import (
	"strings"
	"testing"
	"time"
)

func TestComputeClusterHealthID_Stability(t *testing.T) {
	t.Parallel()

	endpoints := []string{"192.168.1.1", "192.168.1.2"}
	cpNodes := []string{"192.168.1.1"}
	workerNodes := []string{"192.168.1.10", "192.168.1.11"}
	skipK8s := false

	// Same inputs should produce same ID
	id1 := computeClusterHealthID(endpoints, cpNodes, workerNodes, skipK8s)
	id2 := computeClusterHealthID(endpoints, cpNodes, workerNodes, skipK8s)

	if id1 != id2 {
		t.Errorf("Same inputs produced different IDs: %s vs %s", id1, id2)
	}

	// ID should be a valid hex string of 16 chars (8 bytes)
	if len(id1) != 16 {
		t.Errorf("Expected ID length 16, got %d", len(id1))
	}
}

func TestComputeClusterHealthID_OrderIndependence(t *testing.T) {
	t.Parallel()

	// Reordered slices should produce same ID (we sort them internally)
	endpoints1 := []string{"192.168.1.1", "192.168.1.2"}
	endpoints2 := []string{"192.168.1.2", "192.168.1.1"}

	cpNodes := []string{"192.168.1.1"}
	workerNodes := []string{}

	id1 := computeClusterHealthID(endpoints1, cpNodes, workerNodes, false)
	id2 := computeClusterHealthID(endpoints2, cpNodes, workerNodes, false)

	if id1 != id2 {
		t.Errorf("Reordered endpoints produced different IDs: %s vs %s", id1, id2)
	}
}

func TestComputeClusterHealthID_DifferentInputs(t *testing.T) {
	t.Parallel()

	endpoints := []string{"192.168.1.1"}
	cpNodes := []string{"192.168.1.1"}

	// Different skip_kubernetes_checks should produce different ID
	id1 := computeClusterHealthID(endpoints, cpNodes, nil, false)
	id2 := computeClusterHealthID(endpoints, cpNodes, nil, true)

	if id1 == id2 {
		t.Errorf("Different skip_kubernetes_checks produced same ID: %s", id1)
	}

	// Different workers should produce different ID
	id3 := computeClusterHealthID(endpoints, cpNodes, []string{"192.168.1.10"}, false)
	if id1 == id3 {
		t.Errorf("Different workers produced same ID: %s", id1)
	}
}

func TestBackoffInterval(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		iteration int
		initial   time.Duration
		max       time.Duration
		expected  time.Duration
	}{
		{0, 2 * time.Second, 10 * time.Second, 2 * time.Second},
		{1, 2 * time.Second, 10 * time.Second, 4 * time.Second},
		{2, 2 * time.Second, 10 * time.Second, 8 * time.Second},
		{3, 2 * time.Second, 10 * time.Second, 10 * time.Second}, // capped at max
		{4, 2 * time.Second, 10 * time.Second, 10 * time.Second}, // remains at max
		{10, 2 * time.Second, 10 * time.Second, 10 * time.Second},
	}

	for _, tc := range testCases {
		result := backoffInterval(tc.iteration, tc.initial, tc.max)
		if result != tc.expected {
			t.Errorf("backoffInterval(%d, %v, %v) = %v, want %v",
				tc.iteration, tc.initial, tc.max, result, tc.expected)
		}
	}
}

func TestFormatTimeoutError_UnhealthyNodes(t *testing.T) {
	t.Parallel()

	snapshot := &clusterHealthSnapshot{
		Healthy:             false,
		ControlPlaneHealthy: false,
		WorkersHealthy:      true,
		KubernetesHealthy:   false,
		Nodes: []nodeHealthSnapshot{
			{
				Address:        "192.168.1.1",
				Role:           "controlplane",
				Healthy:        false,
				ApidHealthy:    true,
				KubeletHealthy: false,
				EtcdHealthy:    false,
				EtcdMember:     false,
			},
			{
				Address:        "192.168.1.10",
				Role:           "worker",
				Healthy:        true,
				ApidHealthy:    true,
				KubeletHealthy: true,
			},
		},
		Kubernetes: kubernetesHealthSnapshot{
			APIServerHealthy:         false,
			ControllerManagerHealthy: true,
			SchedulerHealthy:         true,
			EtcdHealthy:              false,
			EtcdMembers:              0,
		},
		ReporterOutput: "waiting for etcd cluster to be healthy\n",
	}

	errMsg := formatTimeoutError(snapshot, 10*time.Minute)

	// Verify key components are in the error message
	if !strings.Contains(errMsg, "10m0s") {
		t.Errorf("Expected timeout duration in error message")
	}

	if !strings.Contains(errMsg, "192.168.1.1") {
		t.Errorf("Expected unhealthy node address in error message")
	}

	if !strings.Contains(errMsg, "controlplane") {
		t.Errorf("Expected node role in error message")
	}

	if !strings.Contains(errMsg, "kubelet not ready") {
		t.Errorf("Expected kubelet issue in error message")
	}

	if !strings.Contains(errMsg, "etcd not healthy") {
		t.Errorf("Expected etcd issue in error message")
	}

	if !strings.Contains(errMsg, "API server not responding") {
		t.Errorf("Expected API server issue in error message")
	}

	// Verify healthy node is listed
	if !strings.Contains(errMsg, "192.168.1.10") {
		t.Errorf("Expected healthy node address in error message")
	}

	// Verify reporter output is included
	if !strings.Contains(errMsg, "waiting for etcd") {
		t.Errorf("Expected reporter output in error message")
	}
}

func TestFormatTimeoutError_AllHealthy(t *testing.T) {
	t.Parallel()

	snapshot := &clusterHealthSnapshot{
		Healthy:             true,
		ControlPlaneHealthy: true,
		WorkersHealthy:      true,
		KubernetesHealthy:   true,
		Nodes: []nodeHealthSnapshot{
			{
				Address:        "192.168.1.1",
				Role:           "controlplane",
				Healthy:        true,
				ApidHealthy:    true,
				KubeletHealthy: true,
				EtcdHealthy:    true,
				EtcdMember:     true,
			},
		},
		Kubernetes: kubernetesHealthSnapshot{
			APIServerHealthy:         true,
			ControllerManagerHealthy: true,
			SchedulerHealthy:         true,
			EtcdHealthy:              true,
			EtcdMembers:              1,
		},
	}

	errMsg := formatTimeoutError(snapshot, 5*time.Minute)

	// Should still contain timeout info
	if !strings.Contains(errMsg, "5m0s") {
		t.Errorf("Expected timeout duration in error message")
	}

	// Healthy components section should exist
	if !strings.Contains(errMsg, "Healthy components") {
		t.Errorf("Expected healthy components section in error message")
	}

	// Unhealthy components section should not have content (but header might be absent)
	if strings.Contains(errMsg, "kubelet not ready") {
		t.Errorf("Should not mention kubelet issues when healthy")
	}
}

func TestInitializeSnapshot(t *testing.T) {
	t.Parallel()

	cpNodes := []string{"192.168.1.1", "192.168.1.2"}
	workerNodes := []string{"192.168.1.10"}

	snapshot := initializeSnapshot(cpNodes, workerNodes, false)

	if len(snapshot.Nodes) != 3 {
		t.Errorf("Expected 3 nodes, got %d", len(snapshot.Nodes))
	}

	// Verify roles are set correctly
	for i, node := range snapshot.Nodes {
		if i < 2 {
			if node.Role != "controlplane" {
				t.Errorf("Node %d should be controlplane, got %s", i, node.Role)
			}
		} else {
			if node.Role != "worker" {
				t.Errorf("Node %d should be worker, got %s", i, node.Role)
			}
		}
	}

	// Not skipping k8s checks - kubernetes should not be marked healthy initially
	if snapshot.KubernetesHealthy {
		t.Errorf("KubernetesHealthy should be false when not skipping checks")
	}
}

func TestInitializeSnapshot_SkipK8s(t *testing.T) {
	t.Parallel()

	cpNodes := []string{"192.168.1.1"}
	workerNodes := []string{}

	snapshot := initializeSnapshot(cpNodes, workerNodes, true)

	// When skipping k8s checks, kubernetes should be marked healthy by default
	if !snapshot.KubernetesHealthy {
		t.Errorf("KubernetesHealthy should be true when skipping checks")
	}

	if !snapshot.Kubernetes.APIServerHealthy {
		t.Errorf("APIServerHealthy should be true when skipping checks")
	}

	if !snapshot.Kubernetes.EtcdHealthy {
		t.Errorf("EtcdHealthy should be true when skipping checks")
	}
}

func TestSnapshotToState(t *testing.T) {
	t.Parallel()

	snapshot := &clusterHealthSnapshot{
		Healthy:             true,
		ControlPlaneHealthy: true,
		WorkersHealthy:      true,
		KubernetesHealthy:   true,
		Nodes: []nodeHealthSnapshot{
			{
				Address:        "192.168.1.1",
				Role:           "controlplane",
				Healthy:        true,
				ApidHealthy:    true,
				KubeletHealthy: true,
				EtcdHealthy:    true,
				EtcdMember:     true,
			},
			{
				Address:        "192.168.1.10",
				Role:           "worker",
				Healthy:        true,
				ApidHealthy:    true,
				KubeletHealthy: true,
			},
		},
		Kubernetes: kubernetesHealthSnapshot{
			APIServerHealthy:         true,
			ControllerManagerHealthy: true,
			SchedulerHealthy:         true,
			EtcdHealthy:              true,
			EtcdMembers:              1,
		},
	}

	nodes, k8s := snapshotToState(snapshot)

	if len(nodes) != 2 {
		t.Fatalf("Expected 2 nodes, got %d", len(nodes))
	}

	// Verify control plane node has etcd status
	if nodes[0].Etcd == nil {
		t.Errorf("Control plane node should have etcd status")
	}

	// Verify worker node does not have etcd status
	if nodes[1].Etcd != nil {
		t.Errorf("Worker node should not have etcd status")
	}

	// Verify kubernetes status
	if k8s == nil {
		t.Fatalf("Expected kubernetes status")
	}

	if !k8s.APIServer.Healthy.ValueBool() {
		t.Errorf("APIServer should be healthy")
	}

	if k8s.Etcd.Members.ValueInt64() != 1 {
		t.Errorf("Expected 1 etcd member, got %d", k8s.Etcd.Members.ValueInt64())
	}
}

