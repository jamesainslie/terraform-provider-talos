// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package talos_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTalosClusterHealthDataSource(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.ParallelTest(t, resource.TestCase{
		ExternalProviders: map[string]resource.ExternalProvider{
			"libvirt": {
				Source:            "dmacvicar/libvirt",
				VersionConstraint: "= 0.8.3",
			},
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosClusterHealthDataSourceConfig("talos", rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					// ID should be a deterministic hash (16 hex chars)
					resource.TestMatchResourceAttr("data.talos_cluster_health.this", "id", regexp.MustCompile(`^[a-f0-9]{16}$`)),
					// Overall health flags
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "control_plane_healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "workers_healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes_healthy", "true"),
					// Single control plane node (no workers in this test)
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.#", "1"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.0.role", "controlplane"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.0.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.0.apid.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.0.kubelet.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.0.etcd.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "nodes.0.etcd.member", "true"),
					// Kubernetes component health
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes.api_server.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes.controller_manager.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes.scheduler.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes.etcd.healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes.etcd.members", "1"),
				),
			},
			// make sure there are no changes
			{
				Config:   testAccTalosClusterHealthDataSourceConfig("talos", rName),
				PlanOnly: true,
			},
		},
	})
}

func TestAccTalosClusterHealthDataSource_SkipKubernetesChecks(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.ParallelTest(t, resource.TestCase{
		ExternalProviders: map[string]resource.ExternalProvider{
			"libvirt": {
				Source:            "dmacvicar/libvirt",
				VersionConstraint: "= 0.8.3",
			},
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosClusterHealthDataSourceConfigSkipK8s("talos", rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("data.talos_cluster_health.this", "id", regexp.MustCompile(`^[a-f0-9]{16}$`)),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "healthy", "true"),
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "control_plane_healthy", "true"),
					// When skipping K8s checks, kubernetes_healthy should default to true
					resource.TestCheckResourceAttr("data.talos_cluster_health.this", "kubernetes_healthy", "true"),
				),
			},
		},
	})
}

func testAccTalosClusterHealthDataSourceConfig(providerName, rName string) string {
	config := dynamicConfig{
		Provider:               providerName,
		ResourceName:           rName,
		WithApplyConfig:        true,
		WithBootstrap:          true,
		WithRetrieveKubeConfig: true,
		WithClusterHealth:      true,
	}

	return config.render()
}

func testAccTalosClusterHealthDataSourceConfigSkipK8s(providerName, rName string) string {
	config := dynamicConfig{
		Provider:                   providerName,
		ResourceName:               rName,
		WithApplyConfig:            true,
		WithBootstrap:              true,
		WithRetrieveKubeConfig:     true,
		WithClusterHealthSkipK8s:   true,
	}

	return config.render()
}
