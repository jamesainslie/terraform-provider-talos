// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package talos

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/siderolabs/talos/pkg/cluster"
	"github.com/siderolabs/talos/pkg/cluster/check"
	"github.com/siderolabs/talos/pkg/conditions"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
)

type talosClusterHealthDataSource struct{}

var _ datasource.DataSource = &talosClusterHealthDataSource{}

// talosClusterHealthDataSourceModelV0 is the legacy model (kept for reference).
type talosClusterHealthDataSourceModelV0 struct {
	ID                   types.String        `tfsdk:"id"`
	Endpoints            types.List          `tfsdk:"endpoints"`
	ControlPlaneNodes    types.List          `tfsdk:"control_plane_nodes"`
	WorkerNodes          types.List          `tfsdk:"worker_nodes"`
	ClientConfiguration  clientConfiguration `tfsdk:"client_configuration"`
	Timeouts             timeouts.Value      `tfsdk:"timeouts"`
	SkipKubernetesChecks types.Bool          `tfsdk:"skip_kubernetes_checks"`
}

// talosClusterHealthDataSourceModelV1 is the extended model with rich outputs.
type talosClusterHealthDataSourceModelV1 struct {
	ID                   types.String            `tfsdk:"id"`
	Endpoints            types.List              `tfsdk:"endpoints"`
	ControlPlaneNodes    types.List              `tfsdk:"control_plane_nodes"`
	WorkerNodes          types.List              `tfsdk:"worker_nodes"`
	ClientConfiguration  clientConfiguration     `tfsdk:"client_configuration"`
	Timeouts             timeouts.Value          `tfsdk:"timeouts"`
	SkipKubernetesChecks types.Bool              `tfsdk:"skip_kubernetes_checks"`
	Healthy              types.Bool              `tfsdk:"healthy"`
	ControlPlaneHealthy  types.Bool              `tfsdk:"control_plane_healthy"`
	WorkersHealthy       types.Bool              `tfsdk:"workers_healthy"`
	KubernetesHealthy    types.Bool              `tfsdk:"kubernetes_healthy"`
	Nodes                []nodeHealthStatus      `tfsdk:"nodes"`
	Kubernetes           *kubernetesHealthStatus `tfsdk:"kubernetes"`
}

// nodeHealthStatus represents per-node health status.
type nodeHealthStatus struct {
	Address types.String    `tfsdk:"address"`
	Role    types.String    `tfsdk:"role"`
	Healthy types.Bool      `tfsdk:"healthy"`
	Etcd    *etcdNodeStatus `tfsdk:"etcd"`
	Kubelet *kubeletStatus  `tfsdk:"kubelet"`
	Apid    *apidStatus     `tfsdk:"apid"`
}

// etcdNodeStatus represents etcd status for a node.
type etcdNodeStatus struct {
	Healthy types.Bool `tfsdk:"healthy"`
	Member  types.Bool `tfsdk:"member"`
}

// kubeletStatus represents kubelet status for a node.
type kubeletStatus struct {
	Healthy types.Bool `tfsdk:"healthy"`
}

// apidStatus represents Talos API daemon status for a node.
type apidStatus struct {
	Healthy types.Bool `tfsdk:"healthy"`
}

// kubernetesHealthStatus represents Kubernetes component health.
type kubernetesHealthStatus struct {
	APIServer         *componentHealthStatus   `tfsdk:"api_server"`
	ControllerManager *componentHealthStatus   `tfsdk:"controller_manager"`
	Scheduler         *componentHealthStatus   `tfsdk:"scheduler"`
	Etcd              *etcdClusterHealthStatus `tfsdk:"etcd"`
}

// componentHealthStatus represents a single Kubernetes component's health.
type componentHealthStatus struct {
	Healthy types.Bool `tfsdk:"healthy"`
}

// etcdClusterHealthStatus represents etcd cluster health.
type etcdClusterHealthStatus struct {
	Healthy types.Bool  `tfsdk:"healthy"`
	Members types.Int64 `tfsdk:"members"`
}

// clusterHealthSnapshot captures the current state of cluster health for reporting.
type clusterHealthSnapshot struct {
	Healthy             bool
	ControlPlaneHealthy bool
	WorkersHealthy      bool
	KubernetesHealthy   bool
	Nodes               []nodeHealthSnapshot
	Kubernetes          kubernetesHealthSnapshot
	ReporterOutput      string
	LastError           error
}

type nodeHealthSnapshot struct {
	Address        string
	Role           string
	Healthy        bool
	EtcdHealthy    bool
	EtcdMember     bool
	KubeletHealthy bool
	ApidHealthy    bool
}

type kubernetesHealthSnapshot struct {
	APIServerHealthy         bool
	ControllerManagerHealthy bool
	SchedulerHealthy         bool
	EtcdHealthy              bool
	EtcdMembers              int
}

type clusterNodes struct {
	nodesByType map[machine.Type][]cluster.NodeInfo
	nodes       []cluster.NodeInfo
}

func newClusterNodes(controlPlaneNodes, workerNodes []string) (*clusterNodes, error) {
	controlPlaneNodeInfos, err := cluster.IPsToNodeInfos(controlPlaneNodes)
	if err != nil {
		return nil, err
	}

	workerNodeInfos, err := cluster.IPsToNodeInfos(workerNodes)
	if err != nil {
		return nil, err
	}

	nodesByType := make(map[machine.Type][]cluster.NodeInfo)
	nodesByType[machine.TypeControlPlane] = controlPlaneNodeInfos
	nodesByType[machine.TypeWorker] = workerNodeInfos

	return &clusterNodes{
		nodes:       slices.Concat(controlPlaneNodeInfos, workerNodeInfos),
		nodesByType: nodesByType,
	}, nil
}

// Nodes returns cluster nodeinfos.
func (c *clusterNodes) Nodes() []cluster.NodeInfo {
	return c.nodes
}

// NodesByType returns cluster nodeinfos by type.
func (c *clusterNodes) NodesByType(t machine.Type) []cluster.NodeInfo {
	return c.nodesByType[t]
}

type reporter struct {
	lastLine string
	s        strings.Builder
}

func newReporter() *reporter {
	return &reporter{}
}

// Update implements the conditions.Reporter interface.
func (r *reporter) Update(condition conditions.Condition) {
	if condition.String() != r.lastLine {
		r.s.WriteString(fmt.Sprintf("waiting for %s\n", condition.String()))
		r.lastLine = condition.String()
	}
}

// String returns the string representation of the reporter.
func (r *reporter) String() string {
	return r.s.String()
}

// Reset clears the reporter output.
func (r *reporter) Reset() {
	r.lastLine = ""
	r.s.Reset()
}

// computeClusterHealthID generates a deterministic ID based on input configuration.
func computeClusterHealthID(endpoints, controlPlaneNodes, workerNodes []string, skipK8sChecks bool) string {
	// Sort copies of all slices for deterministic hashing
	sortedEndpoints := make([]string, len(endpoints))
	copy(sortedEndpoints, endpoints)
	sort.Strings(sortedEndpoints)

	sortedCPNodes := make([]string, len(controlPlaneNodes))
	copy(sortedCPNodes, controlPlaneNodes)
	sort.Strings(sortedCPNodes)

	sortedWorkerNodes := make([]string, len(workerNodes))
	copy(sortedWorkerNodes, workerNodes)
	sort.Strings(sortedWorkerNodes)

	input := fmt.Sprintf("endpoints=%v;cp=%v;workers=%v;skip_k8s=%v",
		sortedEndpoints, sortedCPNodes, sortedWorkerNodes, skipK8sChecks)

	hash := sha256.Sum256([]byte(input))

	return fmt.Sprintf("%x", hash[:8])
}

// backoffInterval calculates the next backoff interval with exponential backoff.
// Starts at initialInterval, doubles each iteration, caps at maxInterval.
func backoffInterval(iteration int, initialInterval, maxInterval time.Duration) time.Duration {
	interval := initialInterval
	for i := 0; i < iteration; i++ {
		interval *= 2
		if interval > maxInterval {
			return maxInterval
		}
	}

	return interval
}

// formatTimeoutError builds an actionable error message from a health snapshot.
func formatTimeoutError(snapshot *clusterHealthSnapshot, timeout time.Duration) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Cluster health check timed out after %s\n\n", timeout))

	// Unhealthy components
	var unhealthyParts []string

	for _, node := range snapshot.Nodes {
		if !node.Healthy {
			reasons := []string{}
			if !node.ApidHealthy {
				reasons = append(reasons, "Talos API not responding")
			}
			if !node.KubeletHealthy {
				reasons = append(reasons, "kubelet not ready")
			}
			if node.Role == "controlplane" && !node.EtcdHealthy {
				reasons = append(reasons, "etcd not healthy")
			}

			reasonStr := "unknown"
			if len(reasons) > 0 {
				reasonStr = strings.Join(reasons, ", ")
			}

			unhealthyParts = append(unhealthyParts, fmt.Sprintf("  - Node %s (%s): %s", node.Address, node.Role, reasonStr))
		}
	}

	if !snapshot.Kubernetes.APIServerHealthy {
		unhealthyParts = append(unhealthyParts, "  - Kubernetes: API server not responding")
	}
	if !snapshot.Kubernetes.ControllerManagerHealthy {
		unhealthyParts = append(unhealthyParts, "  - Kubernetes: controller-manager not responding")
	}
	if !snapshot.Kubernetes.SchedulerHealthy {
		unhealthyParts = append(unhealthyParts, "  - Kubernetes: scheduler not responding")
	}
	if !snapshot.Kubernetes.EtcdHealthy {
		unhealthyParts = append(unhealthyParts, fmt.Sprintf("  - Kubernetes: etcd not healthy (%d members)", snapshot.Kubernetes.EtcdMembers))
	}

	if len(unhealthyParts) > 0 {
		sb.WriteString("Unhealthy components:\n")
		sb.WriteString(strings.Join(unhealthyParts, "\n"))
		sb.WriteString("\n\n")
	}

	// Healthy components
	var healthyParts []string

	for _, node := range snapshot.Nodes {
		if node.Healthy {
			healthyParts = append(healthyParts, fmt.Sprintf("  - Node %s (%s): all checks passed", node.Address, node.Role))
		}
	}

	if snapshot.Kubernetes.APIServerHealthy {
		healthyParts = append(healthyParts, "  - Kubernetes API: responding")
	}
	if snapshot.Kubernetes.EtcdHealthy && snapshot.Kubernetes.EtcdMembers > 0 {
		healthyParts = append(healthyParts, fmt.Sprintf("  - etcd: healthy (%d members)", snapshot.Kubernetes.EtcdMembers))
	}

	if len(healthyParts) > 0 {
		sb.WriteString("Healthy components:\n")
		sb.WriteString(strings.Join(healthyParts, "\n"))
		sb.WriteString("\n\n")
	}

	// Include reporter output if available
	if snapshot.ReporterOutput != "" {
		sb.WriteString("Last check status:\n")
		sb.WriteString(snapshot.ReporterOutput)
	}

	if snapshot.LastError != nil {
		sb.WriteString(fmt.Sprintf("\nLast error: %v", snapshot.LastError))
	}

	return sb.String()
}

// snapshotToState converts a health snapshot to Terraform state model.
func snapshotToState(snapshot *clusterHealthSnapshot) ([]nodeHealthStatus, *kubernetesHealthStatus) {
	nodes := make([]nodeHealthStatus, len(snapshot.Nodes))
	for i, n := range snapshot.Nodes {
		node := nodeHealthStatus{
			Address: basetypes.NewStringValue(n.Address),
			Role:    basetypes.NewStringValue(n.Role),
			Healthy: basetypes.NewBoolValue(n.Healthy),
			Kubelet: &kubeletStatus{
				Healthy: basetypes.NewBoolValue(n.KubeletHealthy),
			},
			Apid: &apidStatus{
				Healthy: basetypes.NewBoolValue(n.ApidHealthy),
			},
		}

		// etcd status only for control plane nodes
		if n.Role == "controlplane" {
			node.Etcd = &etcdNodeStatus{
				Healthy: basetypes.NewBoolValue(n.EtcdHealthy),
				Member:  basetypes.NewBoolValue(n.EtcdMember),
			}
		}

		nodes[i] = node
	}

	k8s := &kubernetesHealthStatus{
		APIServer: &componentHealthStatus{
			Healthy: basetypes.NewBoolValue(snapshot.Kubernetes.APIServerHealthy),
		},
		ControllerManager: &componentHealthStatus{
			Healthy: basetypes.NewBoolValue(snapshot.Kubernetes.ControllerManagerHealthy),
		},
		Scheduler: &componentHealthStatus{
			Healthy: basetypes.NewBoolValue(snapshot.Kubernetes.SchedulerHealthy),
		},
		Etcd: &etcdClusterHealthStatus{
			Healthy: basetypes.NewBoolValue(snapshot.Kubernetes.EtcdHealthy),
			Members: basetypes.NewInt64Value(int64(snapshot.Kubernetes.EtcdMembers)),
		},
	}

	return nodes, k8s
}

// NewTalosClusterHealthDataSource implements the datasource.DataSource interface.
func NewTalosClusterHealthDataSource() datasource.DataSource {
	return &talosClusterHealthDataSource{}
}

func (d *talosClusterHealthDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_health"
}

func (d *talosClusterHealthDataSource) Schema(ctx context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Checks the health of a Talos cluster",
		MarkdownDescription: "Waits for the Talos cluster to be healthy. Can be used as a dependency before running other operations on the cluster.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Unique identifier based on a hash of the input configuration.",
			},
			"endpoints": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "endpoints to use for the health check client. Use at least one control plane endpoint.",
			},
			"control_plane_nodes": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "List of control plane nodes to check for health.",
			},
			"worker_nodes": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "List of worker nodes to check for health.",
			},
			"skip_kubernetes_checks": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip Kubernetes component checks, this is useful to check if the nodes has finished booting up and kubelet is running. Default is false.",
			},
			"client_configuration": schema.SingleNestedAttribute{
				Attributes: map[string]schema.Attribute{
					"ca_certificate": schema.StringAttribute{
						Required:    true,
						Description: "The client CA certificate",
					},
					"client_certificate": schema.StringAttribute{
						Required:    true,
						Description: "The client certificate",
					},
					"client_key": schema.StringAttribute{
						Required:    true,
						Sensitive:   true,
						Description: "The client key",
					},
				},
				Required:    true,
				Description: "The client configuration data",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
				Read: true,
			}),
			"healthy": schema.BoolAttribute{
				Computed:    true,
				Description: "True if all health checks passed.",
			},
			"control_plane_healthy": schema.BoolAttribute{
				Computed:    true,
				Description: "True if all control plane nodes are healthy.",
			},
			"workers_healthy": schema.BoolAttribute{
				Computed:    true,
				Description: "True if all worker nodes are healthy (or no workers specified).",
			},
			"kubernetes_healthy": schema.BoolAttribute{
				Computed:    true,
				Description: "True if Kubernetes API and core components are healthy.",
			},
			"nodes": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Per-node health status.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"address": schema.StringAttribute{
							Computed:    true,
							Description: "The node IP address.",
						},
						"role": schema.StringAttribute{
							Computed:    true,
							Description: "The node role (controlplane or worker).",
						},
						"healthy": schema.BoolAttribute{
							Computed:    true,
							Description: "True if all checks for this node passed.",
						},
						"etcd": schema.SingleNestedAttribute{
							Computed:    true,
							Description: "etcd status for this node (control plane only).",
							Attributes: map[string]schema.Attribute{
								"healthy": schema.BoolAttribute{
									Computed:    true,
									Description: "True if etcd is healthy on this node.",
								},
								"member": schema.BoolAttribute{
									Computed:    true,
									Description: "True if this node is a member of the etcd cluster.",
								},
							},
						},
						"kubelet": schema.SingleNestedAttribute{
							Computed:    true,
							Description: "Kubelet status for this node.",
							Attributes: map[string]schema.Attribute{
								"healthy": schema.BoolAttribute{
									Computed:    true,
									Description: "True if kubelet is healthy on this node.",
								},
							},
						},
						"apid": schema.SingleNestedAttribute{
							Computed:    true,
							Description: "Talos API daemon status for this node.",
							Attributes: map[string]schema.Attribute{
								"healthy": schema.BoolAttribute{
									Computed:    true,
									Description: "True if Talos API is responding on this node.",
								},
							},
						},
					},
				},
			},
			"kubernetes": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Kubernetes component health details.",
				Attributes: map[string]schema.Attribute{
					"api_server": schema.SingleNestedAttribute{
						Computed:    true,
						Description: "Kubernetes API server health.",
						Attributes: map[string]schema.Attribute{
							"healthy": schema.BoolAttribute{
								Computed:    true,
								Description: "True if the API server is healthy.",
							},
						},
					},
					"controller_manager": schema.SingleNestedAttribute{
						Computed:    true,
						Description: "Kubernetes controller manager health.",
						Attributes: map[string]schema.Attribute{
							"healthy": schema.BoolAttribute{
								Computed:    true,
								Description: "True if the controller manager is healthy.",
							},
						},
					},
					"scheduler": schema.SingleNestedAttribute{
						Computed:    true,
						Description: "Kubernetes scheduler health.",
						Attributes: map[string]schema.Attribute{
							"healthy": schema.BoolAttribute{
								Computed:    true,
								Description: "True if the scheduler is healthy.",
							},
						},
					},
					"etcd": schema.SingleNestedAttribute{
						Computed:    true,
						Description: "etcd cluster health.",
						Attributes: map[string]schema.Attribute{
							"healthy": schema.BoolAttribute{
								Computed:    true,
								Description: "True if the etcd cluster is healthy.",
							},
							"members": schema.Int64Attribute{
								Computed:    true,
								Description: "Number of etcd cluster members.",
							},
						},
					},
				},
			},
		},
	}
}

func (d *talosClusterHealthDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state talosClusterHealthDataSourceModelV1

	diags := req.Config.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	var (
		endpoints         []string
		controlPlaneNodes []string
		workerNodes       []string
	)

	resp.Diagnostics.Append(state.Endpoints.ElementsAs(ctx, &endpoints, true)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(state.ControlPlaneNodes.ElementsAs(ctx, &controlPlaneNodes, true)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(state.WorkerNodes.ElementsAs(ctx, &workerNodes, true)...)

	if resp.Diagnostics.HasError() {
		return
	}

	readTimeout, diags := state.Timeouts.Read(ctx, 10*time.Minute)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	skipK8sChecks := state.SkipKubernetesChecks.ValueBool()

	talosConfig, err := talosClientTFConfigToTalosClientConfig(
		"dynamic",
		state.ClientConfiguration.CA.ValueString(),
		state.ClientConfiguration.Cert.ValueString(),
		state.ClientConfiguration.Key.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError("failed to generate talos config", err.Error())

		return
	}

	c, err := client.New(ctx, client.WithConfig(talosConfig), client.WithEndpoints(endpoints...))
	if err != nil {
		resp.Diagnostics.AddError("failed to create talos client", err.Error())

		return
	}

	defer c.Close() //nolint:errcheck

	clientProvider := &cluster.ConfigClientProvider{
		DefaultClient: c,
	}
	defer clientProvider.Close() //nolint:errcheck

	nodeInfos, err := newClusterNodes(controlPlaneNodes, workerNodes)
	if err != nil {
		resp.Diagnostics.AddError("failed to generate node infos", err.Error())

		return
	}

	clusterState := struct {
		cluster.ClientProvider
		cluster.K8sProvider
		cluster.Info
	}{
		ClientProvider: clientProvider,
		K8sProvider: &cluster.KubernetesClient{
			ClientProvider: clientProvider,
		},
		Info: nodeInfos,
	}

	// Define checks based on skip_kubernetes_checks
	checks := check.DefaultClusterChecks()
	if skipK8sChecks {
		checks = slices.Concat(check.PreBootSequenceChecks(), check.K8sComponentsReadinessChecks())
	}

	// Initialize snapshot with node info
	snapshot := initializeSnapshot(controlPlaneNodes, workerNodes, skipK8sChecks)

	// Backoff polling parameters
	const (
		initialInterval = 2 * time.Second
		maxInterval     = 10 * time.Second
	)

	overallDeadline := time.Now().Add(readTimeout)
	iteration := 0
	reporter := newReporter()

	for {
		// Check if overall deadline exceeded
		if time.Now().After(overallDeadline) {
			snapshot.ReporterOutput = reporter.String()
			errMsg := formatTimeoutError(snapshot, readTimeout)
			resp.Diagnostics.AddWarning("health check details", reporter.String())
			resp.Diagnostics.AddError("cluster health check timed out", errMsg)

			return
		}

		// Calculate iteration timeout (use backoff interval, but don't exceed remaining time)
		interval := backoffInterval(iteration, initialInterval, maxInterval)
		remaining := time.Until(overallDeadline)
		if interval > remaining {
			interval = remaining
		}

		iterCtx, iterCancel := context.WithTimeout(ctx, interval)
		reporter.Reset()

		tflog.Debug(ctx, fmt.Sprintf("cluster health check iteration %d, interval %s", iteration, interval))

		// Run check with iteration timeout
		checkErr := check.Wait(iterCtx, &clusterState, checks, reporter)
		iterCancel()

		// Update snapshot with current state
		updateSnapshotFromNodes(ctx, c, snapshot, controlPlaneNodes, workerNodes)
		snapshot.ReporterOutput = reporter.String()
		snapshot.LastError = checkErr

		if checkErr == nil {
			// All checks passed - cluster is healthy
			snapshot.Healthy = true
			snapshot.ControlPlaneHealthy = true
			snapshot.WorkersHealthy = true
			if !skipK8sChecks {
				snapshot.KubernetesHealthy = true
				snapshot.Kubernetes.APIServerHealthy = true
				snapshot.Kubernetes.ControllerManagerHealthy = true
				snapshot.Kubernetes.SchedulerHealthy = true
				snapshot.Kubernetes.EtcdHealthy = true
				snapshot.Kubernetes.EtcdMembers = len(controlPlaneNodes)
			}

			// Mark all nodes as healthy
			for i := range snapshot.Nodes {
				snapshot.Nodes[i].Healthy = true
				snapshot.Nodes[i].ApidHealthy = true
				snapshot.Nodes[i].KubeletHealthy = true
				if snapshot.Nodes[i].Role == "controlplane" {
					snapshot.Nodes[i].EtcdHealthy = true
					snapshot.Nodes[i].EtcdMember = true
				}
			}

			break
		}

		// Check if context was cancelled (not just timeout)
		if ctx.Err() != nil {
			resp.Diagnostics.AddError("cluster health check cancelled", ctx.Err().Error())

			return
		}

		// Wait before next iteration (if not at max interval already in context timeout)
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("cluster health check cancelled", ctx.Err().Error())

			return
		case <-time.After(interval):
			// Continue to next iteration
		}

		iteration++
	}

	// Compute deterministic ID
	state.ID = basetypes.NewStringValue(computeClusterHealthID(endpoints, controlPlaneNodes, workerNodes, skipK8sChecks))

	// Set computed outputs
	state.Healthy = basetypes.NewBoolValue(snapshot.Healthy)
	state.ControlPlaneHealthy = basetypes.NewBoolValue(snapshot.ControlPlaneHealthy)
	state.WorkersHealthy = basetypes.NewBoolValue(snapshot.WorkersHealthy)
	state.KubernetesHealthy = basetypes.NewBoolValue(snapshot.KubernetesHealthy)

	nodes, k8s := snapshotToState(snapshot)
	state.Nodes = nodes
	state.Kubernetes = k8s

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

// initializeSnapshot creates a new health snapshot with initial node data.
func initializeSnapshot(controlPlaneNodes, workerNodes []string, skipK8sChecks bool) *clusterHealthSnapshot {
	nodes := make([]nodeHealthSnapshot, 0, len(controlPlaneNodes)+len(workerNodes))

	for _, addr := range controlPlaneNodes {
		nodes = append(nodes, nodeHealthSnapshot{
			Address: addr,
			Role:    "controlplane",
		})
	}

	for _, addr := range workerNodes {
		nodes = append(nodes, nodeHealthSnapshot{
			Address: addr,
			Role:    "worker",
		})
	}

	snapshot := &clusterHealthSnapshot{
		Nodes: nodes,
	}

	// If skipping K8s checks, mark Kubernetes as healthy by default
	if skipK8sChecks {
		snapshot.KubernetesHealthy = true
		snapshot.Kubernetes = kubernetesHealthSnapshot{
			APIServerHealthy:         true,
			ControllerManagerHealthy: true,
			SchedulerHealthy:         true,
			EtcdHealthy:              true,
			EtcdMembers:              len(controlPlaneNodes),
		}
	}

	return snapshot
}

// updateSnapshotFromNodes attempts to probe each node for health status.
// This is a best-effort operation - failures are recorded but don't stop the process.
func updateSnapshotFromNodes(ctx context.Context, c *client.Client, snapshot *clusterHealthSnapshot, controlPlaneNodes, workerNodes []string) {
	allNodes := slices.Concat(controlPlaneNodes, workerNodes)

	for i, node := range snapshot.Nodes {
		if i >= len(allNodes) {
			break
		}

		nodeCtx := client.WithNode(ctx, allNodes[i])

		// Try to get version as a simple connectivity check
		_, err := c.Version(nodeCtx)
		snapshot.Nodes[i].ApidHealthy = err == nil

		// Check service status for kubelet
		// ServiceInfo returns []client.ServiceInfo where each entry has Metadata and Service fields
		servicesResp, err := c.ServiceInfo(nodeCtx, "kubelet")
		if err == nil && servicesResp != nil {
			for _, svcInfo := range servicesResp {
				if svcInfo.Service != nil && svcInfo.Service.Id == "kubelet" && svcInfo.Service.State == "Running" {
					if svcInfo.Service.Health != nil && svcInfo.Service.Health.Healthy {
						snapshot.Nodes[i].KubeletHealthy = true
					}
				}
			}
		}

		// For control plane nodes, check etcd
		if node.Role == "controlplane" {
			etcdResp, err := c.ServiceInfo(nodeCtx, "etcd")
			if err == nil && etcdResp != nil {
				for _, svcInfo := range etcdResp {
					if svcInfo.Service != nil && svcInfo.Service.Id == "etcd" && svcInfo.Service.State == "Running" {
						if svcInfo.Service.Health != nil && svcInfo.Service.Health.Healthy {
							snapshot.Nodes[i].EtcdHealthy = true
							snapshot.Nodes[i].EtcdMember = true
						}
					}
				}
			}
		}

		// Node is healthy if all its components are healthy
		snapshot.Nodes[i].Healthy = snapshot.Nodes[i].ApidHealthy && snapshot.Nodes[i].KubeletHealthy
		if node.Role == "controlplane" {
			snapshot.Nodes[i].Healthy = snapshot.Nodes[i].Healthy && snapshot.Nodes[i].EtcdHealthy
		}
	}

	// Update aggregate health
	snapshot.ControlPlaneHealthy = true
	snapshot.WorkersHealthy = true

	for _, node := range snapshot.Nodes {
		if node.Role == "controlplane" && !node.Healthy {
			snapshot.ControlPlaneHealthy = false
		}
		if node.Role == "worker" && !node.Healthy {
			snapshot.WorkersHealthy = false
		}
	}

	// Count etcd members
	etcdMembers := 0
	for _, node := range snapshot.Nodes {
		if node.Role == "controlplane" && node.EtcdMember {
			etcdMembers++
		}
	}

	snapshot.Kubernetes.EtcdMembers = etcdMembers
}
