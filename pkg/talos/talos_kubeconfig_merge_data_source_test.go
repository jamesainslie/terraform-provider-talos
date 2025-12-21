// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package talos_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTalosKubeconfigMergeDataSource_Basic(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosKubeconfigMergeBasicConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "cluster_name_out", "test-cluster"),
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "context_name_out", "admin@test-cluster"),
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "user_name_out", "admin@test-cluster"),
					resource.TestCheckResourceAttrSet("data.talos_kubeconfig_merge.test", "merged_kubeconfig_raw"),
					resource.TestCheckResourceAttrSet("data.talos_kubeconfig_merge.test", "id"),
				),
			},
		},
	})
}

func TestAccTalosKubeconfigMergeDataSource_WithClusterNameOverride(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosKubeconfigMergeWithNameOverrideConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "cluster_name_out", "my-custom-cluster"),
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "context_name_out", "my-custom-cluster"),
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "user_name_out", "my-custom-cluster"),
				),
			},
		},
	})
}

func TestAccTalosKubeconfigMergeDataSource_MergeWithExisting(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosKubeconfigMergeMergeWithExistingConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "cluster_name_out", "test-cluster"),
					// Verify backup contains the original
					resource.TestCheckResourceAttrSet("data.talos_kubeconfig_merge.test", "base_kubeconfig_backup"),
				),
			},
		},
	})
}

func TestAccTalosKubeconfigMergeDataSource_SetCurrent(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosKubeconfigMergeSetCurrentConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "cluster_name_out", "test-cluster"),
				),
			},
		},
	})
}

func TestAccTalosKubeconfigMergeDataSource_FromFile(t *testing.T) {
	// Create a temporary kubeconfig file
	tmpDir := t.TempDir()
	kubeconfigPath := filepath.Join(tmpDir, "config")

	existingKubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://existing-cluster:6443
    certificate-authority-data: ZXhpc3RpbmctY2E=
  name: existing-cluster
contexts:
- context:
    cluster: existing-cluster
    user: existing-user
  name: existing-context
current-context: existing-context
users:
- name: existing-user
  user:
    client-certificate-data: ZXhpc3RpbmctY2VydA==
    client-key-data: ZXhpc3Rpbmcta2V5
`

	err := os.WriteFile(kubeconfigPath, []byte(existingKubeconfig), 0600)
	if err != nil {
		t.Fatalf("failed to write test kubeconfig: %v", err)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccTalosKubeconfigMergeFromFileConfig(kubeconfigPath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.talos_kubeconfig_merge.test", "cluster_name_out", "test-cluster"),
					// Backup should contain the existing config
					resource.TestCheckResourceAttrSet("data.talos_kubeconfig_merge.test", "base_kubeconfig_backup"),
				),
			},
		},
	})
}

func testAccTalosKubeconfigMergeBasicConfig() string {
	return `
data "talos_kubeconfig_merge" "test" {
  kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://192.168.1.10:6443
    certificate-authority-data: dGVzdC1jYQ==
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: admin@test-cluster
  name: admin@test-cluster
current-context: admin@test-cluster
users:
- name: admin@test-cluster
  user:
    client-certificate-data: dGVzdC1jZXJ0
    client-key-data: dGVzdC1rZXk=
EOT
}
`
}

func testAccTalosKubeconfigMergeWithNameOverrideConfig() string {
	return `
data "talos_kubeconfig_merge" "test" {
  kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://192.168.1.10:6443
    certificate-authority-data: dGVzdC1jYQ==
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: admin@test-cluster
  name: admin@test-cluster
current-context: admin@test-cluster
users:
- name: admin@test-cluster
  user:
    client-certificate-data: dGVzdC1jZXJ0
    client-key-data: dGVzdC1rZXk=
EOT

  cluster_name = "my-custom-cluster"
}
`
}

func testAccTalosKubeconfigMergeMergeWithExistingConfig() string {
	return `
data "talos_kubeconfig_merge" "test" {
  kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://192.168.1.10:6443
    certificate-authority-data: dGVzdC1jYQ==
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: admin@test-cluster
  name: admin@test-cluster
current-context: admin@test-cluster
users:
- name: admin@test-cluster
  user:
    client-certificate-data: dGVzdC1jZXJ0
    client-key-data: dGVzdC1rZXk=
EOT

  base_kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://existing-cluster:6443
    certificate-authority-data: ZXhpc3RpbmctY2E=
  name: existing-cluster
contexts:
- context:
    cluster: existing-cluster
    user: existing-user
  name: existing-context
current-context: existing-context
users:
- name: existing-user
  user:
    client-certificate-data: ZXhpc3RpbmctY2VydA==
    client-key-data: ZXhpc3Rpbmcta2V5
EOT
}
`
}

func testAccTalosKubeconfigMergeSetCurrentConfig() string {
	return `
data "talos_kubeconfig_merge" "test" {
  kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://192.168.1.10:6443
    certificate-authority-data: dGVzdC1jYQ==
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: admin@test-cluster
  name: admin@test-cluster
current-context: admin@test-cluster
users:
- name: admin@test-cluster
  user:
    client-certificate-data: dGVzdC1jZXJ0
    client-key-data: dGVzdC1rZXk=
EOT

  base_kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://existing-cluster:6443
    certificate-authority-data: ZXhpc3RpbmctY2E=
  name: existing-cluster
contexts:
- context:
    cluster: existing-cluster
    user: existing-user
  name: existing-context
current-context: existing-context
users:
- name: existing-user
  user:
    client-certificate-data: ZXhpc3RpbmctY2VydA==
    client-key-data: ZXhpc3Rpbmcta2V5
EOT

  set_current = true
}
`
}

func testAccTalosKubeconfigMergeFromFileConfig(kubeconfigPath string) string {
	return `
data "talos_kubeconfig_merge" "test" {
  kubeconfig_raw = <<-EOT
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://192.168.1.10:6443
    certificate-authority-data: dGVzdC1jYQ==
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: admin@test-cluster
  name: admin@test-cluster
current-context: admin@test-cluster
users:
- name: admin@test-cluster
  user:
    client-certificate-data: dGVzdC1jZXJ0
    client-key-data: dGVzdC1rZXk=
EOT

  base_kubeconfig_path = "` + kubeconfigPath + `"
}
`
}

