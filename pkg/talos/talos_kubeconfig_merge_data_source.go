// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package talos

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

type talosKubeconfigMergeDataSource struct{}

var (
	_ datasource.DataSource              = &talosKubeconfigMergeDataSource{}
	_ datasource.DataSourceWithValidateConfig = &talosKubeconfigMergeDataSource{}
)

type talosKubeconfigMergeDataSourceModel struct {
	ID                   types.String `tfsdk:"id"`
	KubeconfigRaw        types.String `tfsdk:"kubeconfig_raw"`
	BaseKubeconfigPath   types.String `tfsdk:"base_kubeconfig_path"`
	BaseKubeconfigRaw    types.String `tfsdk:"base_kubeconfig_raw"`
	ClusterName          types.String `tfsdk:"cluster_name"`
	SetCurrent           types.Bool   `tfsdk:"set_current"`
	MergedKubeconfigRaw  types.String `tfsdk:"merged_kubeconfig_raw"`
	BaseKubeconfigBackup types.String `tfsdk:"base_kubeconfig_backup"`
	ClusterNameOut       types.String `tfsdk:"cluster_name_out"`
	ContextNameOut       types.String `tfsdk:"context_name_out"`
	UserNameOut          types.String `tfsdk:"user_name_out"`
}

// NewTalosKubeconfigMergeDataSource implements the datasource.DataSource interface.
func NewTalosKubeconfigMergeDataSource() datasource.DataSource {
	return &talosKubeconfigMergeDataSource{}
}

func (d *talosKubeconfigMergeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubeconfig_merge"
}

func (d *talosKubeconfigMergeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Merges a Talos cluster kubeconfig with an existing kubeconfig file non-destructively.",
		MarkdownDescription: `Merges a Talos cluster kubeconfig with an existing kubeconfig file non-destructively.

This data source takes the kubeconfig from a Talos cluster and merges it with an existing kubeconfig,
allowing you to manage multiple clusters in a single kubeconfig file. The merged output can then be
written to disk using a ` + "`local_file`" + ` resource.

The original base kubeconfig is preserved in ` + "`base_kubeconfig_backup`" + ` for safety.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Unique identifier based on a hash of the merged configuration.",
			},
			"kubeconfig_raw": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "The kubeconfig YAML from the Talos cluster to merge.",
			},
			"base_kubeconfig_path": schema.StringAttribute{
				Optional:    true,
				Description: "Path to the existing kubeconfig file to merge with. Supports ~ for home directory. Mutually exclusive with base_kubeconfig_raw.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.MatchRoot("base_kubeconfig_raw")),
				},
			},
			"base_kubeconfig_raw": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Raw content of an existing kubeconfig to merge with. Mutually exclusive with base_kubeconfig_path.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.MatchRoot("base_kubeconfig_path")),
				},
			},
			"cluster_name": schema.StringAttribute{
				Optional:    true,
				Description: "Override name for the cluster, context, and user entries. If not set, uses the names from kubeconfig_raw.",
			},
			"set_current": schema.BoolAttribute{
				Optional:    true,
				Description: "Whether to set the merged context as current-context. Defaults to false.",
			},
			"merged_kubeconfig_raw": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "The merged kubeconfig YAML.",
			},
			"base_kubeconfig_backup": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "The original base kubeconfig content before merging. Empty if no base was provided.",
			},
			"cluster_name_out": schema.StringAttribute{
				Computed:    true,
				Description: "The name of the cluster entry that was added/updated.",
			},
			"context_name_out": schema.StringAttribute{
				Computed:    true,
				Description: "The name of the context entry that was added/updated.",
			},
			"user_name_out": schema.StringAttribute{
				Computed:    true,
				Description: "The name of the user entry that was added/updated.",
			},
		},
	}
}

func (d *talosKubeconfigMergeDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config talosKubeconfigMergeDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Validation is handled by schema validators (ConflictsWith)
}

func (d *talosKubeconfigMergeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state talosKubeconfigMergeDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Parse the new kubeconfig from Talos
	newKubeconfig, err := clientcmd.Load([]byte(state.KubeconfigRaw.ValueString()))
	if err != nil {
		resp.Diagnostics.AddError("failed to parse kubeconfig_raw", err.Error())

		return
	}

	// Get the base kubeconfig (either from path, raw content, or create empty)
	var baseKubeconfig *api.Config
	var baseKubeconfigContent string

	if !state.BaseKubeconfigPath.IsNull() && state.BaseKubeconfigPath.ValueString() != "" {
		expandedPath := expandPath(state.BaseKubeconfigPath.ValueString())

		content, err := os.ReadFile(expandedPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File doesn't exist, start with empty config
				baseKubeconfig = api.NewConfig()
				baseKubeconfigContent = ""
			} else {
				resp.Diagnostics.AddError("failed to read base kubeconfig file", err.Error())

				return
			}
		} else {
			baseKubeconfigContent = string(content)

			baseKubeconfig, err = clientcmd.Load(content)
			if err != nil {
				resp.Diagnostics.AddError("failed to parse base kubeconfig file", err.Error())

				return
			}
		}
	} else if !state.BaseKubeconfigRaw.IsNull() && state.BaseKubeconfigRaw.ValueString() != "" {
		baseKubeconfigContent = state.BaseKubeconfigRaw.ValueString()

		baseKubeconfig, err = clientcmd.Load([]byte(baseKubeconfigContent))
		if err != nil {
			resp.Diagnostics.AddError("failed to parse base_kubeconfig_raw", err.Error())

			return
		}
	} else {
		// No base provided, start with empty config
		baseKubeconfig = api.NewConfig()
		baseKubeconfigContent = ""
	}

	// Store the backup
	state.BaseKubeconfigBackup = basetypes.NewStringValue(baseKubeconfigContent)

	// Extract cluster, context, and user from the new kubeconfig
	if newKubeconfig.CurrentContext == "" {
		resp.Diagnostics.AddError("invalid kubeconfig_raw", "kubeconfig has no current-context set")

		return
	}

	newContext, ok := newKubeconfig.Contexts[newKubeconfig.CurrentContext]
	if !ok {
		resp.Diagnostics.AddError("invalid kubeconfig_raw", fmt.Sprintf("context %q not found", newKubeconfig.CurrentContext))

		return
	}

	originalClusterName := newContext.Cluster
	originalUserName := newContext.AuthInfo
	originalContextName := newKubeconfig.CurrentContext

	newCluster, ok := newKubeconfig.Clusters[originalClusterName]
	if !ok {
		resp.Diagnostics.AddError("invalid kubeconfig_raw", fmt.Sprintf("cluster %q not found", originalClusterName))

		return
	}

	newUser, ok := newKubeconfig.AuthInfos[originalUserName]
	if !ok {
		resp.Diagnostics.AddError("invalid kubeconfig_raw", fmt.Sprintf("user %q not found", originalUserName))

		return
	}

	// Determine the names to use (override or original)
	clusterName := originalClusterName
	userName := originalUserName
	contextName := originalContextName

	if !state.ClusterName.IsNull() && state.ClusterName.ValueString() != "" {
		clusterName = state.ClusterName.ValueString()
		userName = state.ClusterName.ValueString()
		contextName = state.ClusterName.ValueString()
	}

	// Initialize maps if nil
	if baseKubeconfig.Clusters == nil {
		baseKubeconfig.Clusters = make(map[string]*api.Cluster)
	}

	if baseKubeconfig.AuthInfos == nil {
		baseKubeconfig.AuthInfos = make(map[string]*api.AuthInfo)
	}

	if baseKubeconfig.Contexts == nil {
		baseKubeconfig.Contexts = make(map[string]*api.Context)
	}

	// Merge the new entries into the base config
	baseKubeconfig.Clusters[clusterName] = newCluster
	baseKubeconfig.AuthInfos[userName] = newUser
	baseKubeconfig.Contexts[contextName] = &api.Context{
		Cluster:  clusterName,
		AuthInfo: userName,
	}

	// Set current context if requested
	setCurrent := false
	if !state.SetCurrent.IsNull() {
		setCurrent = state.SetCurrent.ValueBool()
	}

	if setCurrent {
		baseKubeconfig.CurrentContext = contextName
	}

	// Serialize the merged config
	mergedBytes, err := clientcmd.Write(*baseKubeconfig)
	if err != nil {
		resp.Diagnostics.AddError("failed to serialize merged kubeconfig", err.Error())

		return
	}

	// Set outputs
	state.MergedKubeconfigRaw = basetypes.NewStringValue(string(mergedBytes))
	state.ClusterNameOut = basetypes.NewStringValue(clusterName)
	state.ContextNameOut = basetypes.NewStringValue(contextName)
	state.UserNameOut = basetypes.NewStringValue(userName)

	// Generate deterministic ID
	state.ID = basetypes.NewStringValue(computeKubeconfigMergeID(clusterName, contextName, userName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// expandPath expands ~ to home directory and resolves environment variables.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	return os.ExpandEnv(path)
}

// computeKubeconfigMergeID generates a deterministic ID for the merged kubeconfig.
func computeKubeconfigMergeID(clusterName, contextName, userName string) string {
	input := fmt.Sprintf("cluster=%s;context=%s;user=%s", clusterName, contextName, userName)
	hash := sha256.Sum256([]byte(input))

	return fmt.Sprintf("%x", hash[:8])
}

