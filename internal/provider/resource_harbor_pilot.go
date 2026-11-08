package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- Harbor Pilot connection (SpaceMan ADR-0060) ----

// harborPilotImportID is the import ID of the connection: there is one per central.
const harborPilotImportID = "harbor-pilot"

type harborPilotConnectionResource struct{ base }

func newHarborPilotConnectionResource() resource.Resource { return &harborPilotConnectionResource{} }

type harborPilotConnectionModel struct {
	ID                    types.String `tfsdk:"id"`
	ConnectionID          types.String `tfsdk:"connection_id"`
	BaseURL               types.String `tfsdk:"base_url"`
	RunBaseURL            types.String `tfsdk:"run_base_url"`
	AllowInsecureHTTP     types.Bool   `tfsdk:"allow_insecure_http"`
	ParentTokenWO         types.String `tfsdk:"parent_token_wo"`
	ParentTokenWOVersion  types.Int64  `tfsdk:"parent_token_wo_version"`
	ServiceTokenWO        types.String `tfsdk:"service_token_wo"`
	ServiceTokenWOVersion types.Int64  `tfsdk:"service_token_wo_version"`
	BindingID             types.String `tfsdk:"binding_id"`
	HasParentToken        types.Bool   `tfsdk:"has_parent_token"`
	HasServiceToken       types.Bool   `tfsdk:"has_service_token"`
	ParentTokenVersion    types.Int64  `tfsdk:"parent_token_version"`
	ServiceTokenVersion   types.Int64  `tfsdk:"service_token_version"`
	ParentExpiresAt       types.String `tfsdk:"parent_expires_at"`
}

func (r *harborPilotConnectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harbor_pilot_connection"
}

func (r *harborPilotConnectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computedInt := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Central's connection to a [Harbor Pilot](https://aurimasniekis.github.io/go-harbor-pilot/) gateway " +
			"(`/harbor-pilot/connection`, SpaceMan ADR-0060). There is one per central. Spaces opt in with `spaceman_space_harbor_pilot`; their runs " +
			"then get a short-lived child token minted from the parent token instead of a harness account.\n\n" +
			"Both tokens are write-only: sealed by SpaceMan, never returned and never stored by Terraform (requires Terraform 1.11 or later). " +
			"They fit Harbor Pilot's ephemeral `harborpilot_gateway_token` and `harborpilot_service_principal_token` resources. Every write is " +
			"validated against Harbor Pilot (a probe mint, revoked at once), so the gateway must be reachable from central. Destroying the " +
			"connection is refused while a space routes runs through Harbor Pilot or run tokens are live. Needs `account:write`. Import with " +
			"the ID `" + harborPilotImportID + "`.",
		Attributes: map[string]schema.Attribute{
			"id": idAttr("Always `" + harborPilotImportID + "` (the connection is a singleton)."),
			"connection_id": schema.StringAttribute{Computed: true, MarkdownDescription: "SpaceMan's ID of the connection.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"base_url": schema.StringAttribute{Required: true, MarkdownDescription: "The gateway origin central calls to mint and revoke run tokens " +
				"and read budget status, e.g. `https://harbor-pilot.example.com`. `https` unless the host is loopback or `allow_insecure_http` is " +
				"set. Changing it sends both tokens again: SpaceMan sends stored tokens only to the address they were entered for."},
			"run_base_url": optString("The origin runs reach the gateway on, when it differs from `base_url` (for example when central reaches " +
				"it on loopback). It needs `https` (no loopback exemption) unless `allow_insecure_http` is set. Empty: `base_url`."),
			"allow_insecure_http": optBool("Allow plain `http` (tests and trusted local networks only: every token then crosses the network " +
				"in clear text). Default `false`."),
			"parent_token_wo": schema.StringAttribute{Required: true, WriteOnly: true, Sensitive: true,
				MarkdownDescription: "A Harbor Pilot gateway credential (`hpk_…`) with `gateway:invoke`, `gateway:models` and `tokens:mint` " +
					"(write-only). Sent on create, whenever `parent_token_wo_version` changes and whenever the gateway address changes."},
			"parent_token_wo_version": woVersion("parent_token_wo"),
			"service_token_wo": schema.StringAttribute{Optional: true, WriteOnly: true, Sensitive: true,
				MarkdownDescription: "A Harbor Pilot service-principal token (`hpa_…`) with `usage.read` and `config.read` (write-only), for " +
					"budget status and project checks. Sent on create, whenever `service_token_wo_version` changes and whenever the gateway " +
					"address changes; left out at those times, the stored service token is removed."},
			"service_token_wo_version": woVersion("service_token_wo"),
			"binding_id":               schema.StringAttribute{Computed: true, MarkdownDescription: "The parent token's Harbor Pilot client binding, learnt when SpaceMan validated the connection."},
			"has_parent_token":         schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether SpaceMan holds a parent token."},
			"has_service_token":        schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether SpaceMan holds a service-principal token."},
			"parent_token_version":     computedInt("SpaceMan's counter of parent token writes."),
			"service_token_version":    computedInt("SpaceMan's counter of service-principal token writes and removals."),
			"parent_expires_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "When the parent token expires (RFC 3339), if it does."},
		},
	}
}

// keepURL keeps the configured spelling of a URL the server normalised (a trailing slash).
func keepURL(prior types.String, server string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() && strings.TrimRight(strings.TrimSpace(prior.ValueString()), "/") == strings.TrimRight(server, "/") {
		return prior
	}
	return types.StringValue(server)
}

func (r *harborPilotConnectionResource) toModel(c *client.HarborPilotConnection, m *harborPilotConnectionModel) {
	m.ID = types.StringValue(harborPilotImportID)
	m.ConnectionID = types.StringValue(c.ID)
	m.BaseURL = keepURL(m.BaseURL, c.BaseURL)
	m.RunBaseURL = keepURL(m.RunBaseURL, c.RunBaseURL)
	m.AllowInsecureHTTP = types.BoolValue(c.AllowInsecureHTTP)
	m.ParentTokenWO = types.StringNull()
	m.ServiceTokenWO = types.StringNull()
	m.BindingID = types.StringValue(c.BindingID)
	m.HasParentToken = types.BoolValue(c.HasParentToken)
	m.HasServiceToken = types.BoolValue(c.HasServiceToken)
	m.ParentTokenVersion = types.Int64Value(c.ParentTokenVersion)
	m.ServiceTokenVersion = types.Int64Value(c.ServiceTokenVersion)
	m.ParentExpiresAt = types.StringNull()
	if c.ParentExpiresAt != nil {
		m.ParentExpiresAt = types.StringValue(c.ParentExpiresAt.UTC().Format(time.RFC3339))
	}
}

func (r *harborPilotConnectionResource) input(m *harborPilotConnectionModel) client.HarborPilotConnectionInput {
	return client.HarborPilotConnectionInput{BaseURL: strings.TrimSpace(m.BaseURL.ValueString()), RunBaseURL: strOrEmpty(m.RunBaseURL),
		AllowInsecureHTTP: m.AllowInsecureHTTP.ValueBool()}
}

// configTokens reads the write-only tokens from the configuration (they are never in the plan or the state).
func configTokens(ctx context.Context, cfg tfsdk.Config, d *diagnostics) (parent, service string) {
	var p, s types.String
	d.Append(cfg.GetAttribute(ctx, path.Root("parent_token_wo"), &p)...)
	d.Append(cfg.GetAttribute(ctx, path.Root("service_token_wo"), &s)...)
	return strOrEmpty(p), strOrEmpty(s)
}

func addWarnings(d *diagnostics, summary string, warnings []string) {
	for _, w := range warnings {
		d.AddWarning(summary, w)
	}
}

func (r *harborPilotConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m harborPilotConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	parent, service := configTokens(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	in := r.input(&m)
	in.ParentToken, in.ServiceToken = parent, service
	c, err := r.c.PutHarborPilotConnection(ctx, 0, in)
	if err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("A Harbor Pilot connection already exists",
				fmt.Sprintf("%v\n\nSpaceMan has one connection per central. Import it with `terraform import <address> %s`.", err, harborPilotImportID))
			return
		}
		addAPIError(&resp.Diagnostics, "Could not connect Harbor Pilot", err)
		return
	}
	addWarnings(&resp.Diagnostics, "Harbor Pilot connection", c.Warnings)
	r.toModel(c, &m)
	setVersion(ctx, resp.Private, c.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *harborPilotConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m harborPilotConnectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.c.GetHarborPilotConnection(ctx)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Harbor Pilot connection", err)
		return
	}
	r.toModel(c, &m)
	setVersion(ctx, resp.Private, c.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// harborPilotAddressChanged mirrors SpaceMan's rule: a new base_url or run_base_url, or newly allowing plain http, needs
// the tokens sent again.
func harborPilotAddressChanged(plan, prior *harborPilotConnectionModel) bool {
	norm := func(v types.String) string { return strings.TrimRight(strings.TrimSpace(strOrEmpty(v)), "/") }
	return norm(plan.BaseURL) != norm(prior.BaseURL) || norm(plan.RunBaseURL) != norm(prior.RunBaseURL) ||
		(plan.AllowInsecureHTTP.ValueBool() && !prior.AllowInsecureHTTP.ValueBool())
}

// ModifyPlan keeps the computed token facts from the state when an update sends no token: only a token write changes
// them, so a plan that only changes, say, run_base_url doesn't show them as unknown.
func (r *harborPilotConnectionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, prior harborPilotConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	moved := harborPilotAddressChanged(&plan, &prior)
	if !moved && plan.ParentTokenWOVersion.Equal(prior.ParentTokenWOVersion) {
		plan.BindingID, plan.HasParentToken, plan.ParentTokenVersion, plan.ParentExpiresAt =
			prior.BindingID, prior.HasParentToken, prior.ParentTokenVersion, prior.ParentExpiresAt
	}
	if !moved && plan.ServiceTokenWOVersion.Equal(prior.ServiceTokenWOVersion) {
		plan.HasServiceToken, plan.ServiceTokenVersion = prior.HasServiceToken, prior.ServiceTokenVersion
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *harborPilotConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior harborPilotConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	parent, service := configTokens(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	in := r.input(&m)
	moved := harborPilotAddressChanged(&m, &prior)
	if moved || !m.ParentTokenWOVersion.Equal(prior.ParentTokenWOVersion) {
		in.ParentToken = parent
	}
	if moved || !m.ServiceTokenWOVersion.Equal(prior.ServiceTokenWOVersion) {
		in.ServiceToken = service
		in.RemoveServiceToken = service == "" && prior.HasServiceToken.ValueBool()
	}
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetHarborPilotConnection(ctx)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the Harbor Pilot connection", err)
			return
		}
		v = cur.Version
	}
	c, err := r.c.PutHarborPilotConnection(ctx, v, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not update the Harbor Pilot connection", err)
		return
	}
	addWarnings(&resp.Diagnostics, "Harbor Pilot connection", c.Warnings)
	r.toModel(c, &m)
	setVersion(ctx, resp.Private, c.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *harborPilotConnectionResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	cur, err := r.c.GetHarborPilotConnection(ctx)
	if client.IsNotFound(err) {
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Harbor Pilot connection", err)
		return
	}
	if err := r.c.DeleteHarborPilotConnection(ctx, cur.Version); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the Harbor Pilot connection", err)
	}
}

func (r *harborPilotConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != harborPilotImportID {
		resp.Diagnostics.AddError("Unexpected import ID", fmt.Sprintf("The Harbor Pilot connection is a singleton: import it with the ID %q, not %q.", harborPilotImportID, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), harborPilotImportID)...)
}

// ---- a space's Harbor Pilot settings ----

type spaceHarborPilotResource struct{ base }

func newSpaceHarborPilotResource() resource.Resource { return &spaceHarborPilotResource{} }

type spaceHarborPilotModel struct {
	ID                        types.String `tfsdk:"id"`
	SpaceID                   types.String `tfsdk:"space_id"`
	Enabled                   types.Bool   `tfsdk:"enabled"`
	ProjectID                 types.String `tfsdk:"project_id"`
	Harnesses                 types.Set    `tfsdk:"harnesses"`
	TTLSeconds                types.Int64  `tfsdk:"ttl_seconds"`
	OnBudgetStatusUnavailable types.String `tfsdk:"on_budget_status_unavailable"`
	AllowedModelAliases       types.Set    `tfsdk:"allowed_model_aliases"`
	Connected                 types.Bool   `tfsdk:"connected"`
}

func (r *spaceHarborPilotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_space_harbor_pilot"
}

func (r *spaceHarborPilotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A space's Harbor Pilot opt-in (`/spaces/{id}/harbor-pilot`, SpaceMan ADR-0060). For the listed harnesses each run " +
			"gets its own short-lived Harbor Pilot token, bound to the space, the run and the project, in place of a harness account: there is " +
			"no fallback to a direct account. Enabling needs `spaceman_harbor_pilot_connection`. Needs `space_config:write`; setting or " +
			"changing `project_id` (who pays) needs `account:write`. Destroying the resource removes the settings, and the space's runs use " +
			"their accounts again. Import with the space ID.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttr("The space ID."),
			"space_id": requiredReplace("The space."),
			"enabled":  schema.BoolAttribute{Required: true, MarkdownDescription: "Whether the space's runs of the listed harnesses go through Harbor Pilot."},
			"project_id": optString("The Harbor Pilot project the space's runs are charged to (required when `enabled`). It must be the " +
				"connection binding's project or one of its `allowed_project_ids`. Removing it from the configuration keeps the stored project."),
			"harnesses": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Harness kinds routed through Harbor Pilot; the others keep their accounts. Default `claude-code` and `codex`.",
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"ttl_seconds": schema.Int64Attribute{Optional: true, Computed: true,
				MarkdownDescription: "Lifetime of a run's token, 900 to 604800 seconds (default 86400). A run that outlives it loses model access.",
				Validators:          []validator.Int64{int64validator.Between(900, 604800)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"on_budget_status_unavailable": optString("What a launch does when Harbor Pilot budget status can't be read: `proceed` (default; "+
				"the gateway still enforces its budgets) or `refuse`.", stringvalidator.OneOf("proceed", "refuse")),
			"allowed_model_aliases": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Model aliases the space's runs may use; empty (default): any model the binding's plan routes.",
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"connected": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether a Harbor Pilot connection exists."},
		},
	}
}

func (r *spaceHarborPilotResource) toModel(s *client.SpaceHarborPilot, m *spaceHarborPilotModel) {
	m.ID = types.StringValue(s.SpaceID)
	m.SpaceID = types.StringValue(s.SpaceID)
	m.Enabled = types.BoolValue(s.Enabled)
	m.ProjectID = types.StringValue(s.ProjectID)
	m.Harnesses = setValue(s.Harnesses)
	m.TTLSeconds = types.Int64Value(s.TTLSeconds)
	m.OnBudgetStatusUnavailable = types.StringValue(s.OnBudgetStatusUnavailable)
	m.AllowedModelAliases = setValue(s.AllowedModelAliases)
	m.Connected = types.BoolValue(s.Connected)
}

func (r *spaceHarborPilotResource) input(ctx context.Context, m *spaceHarborPilotModel, d *diagnostics) client.SpaceHarborPilotInput {
	return client.SpaceHarborPilotInput{Enabled: m.Enabled.ValueBool(), ProjectID: strOrEmpty(m.ProjectID), Harnesses: stringSet(ctx, m.Harnesses, d),
		TTLSeconds: int64OrZero(m.TTLSeconds), OnBudgetStatusUnavailable: strOrEmpty(m.OnBudgetStatusUnavailable),
		AllowedModelAliases: stringSet(ctx, m.AllowedModelAliases, d)}
}

func (r *spaceHarborPilotResource) save(ctx context.Context, m *spaceHarborPilotModel, version int64, d *diagnostics) (*client.SpaceHarborPilot, bool) {
	in := r.input(ctx, m, d)
	if d.HasError() {
		return nil, false
	}
	s, err := r.c.PutSpaceHarborPilot(ctx, m.SpaceID.ValueString(), version, in)
	if err != nil {
		addAPIError(d, "Could not save the space's Harbor Pilot settings", err)
		return nil, false
	}
	addWarnings(d, "Harbor Pilot settings", s.Warnings)
	return s, true
}

func (r *spaceHarborPilotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m spaceHarborPilotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.c.GetSpaceHarborPilot(ctx, m.SpaceID.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's Harbor Pilot settings", err)
		return
	}
	if cur.Stored {
		resp.Diagnostics.AddError("The space already has Harbor Pilot settings",
			fmt.Sprintf("Import them with `terraform import <address> %s`, or remove them in SpaceMan first.", m.SpaceID.ValueString()))
		return
	}
	s, ok := r.save(ctx, &m, 0, &resp.Diagnostics)
	if !ok {
		return
	}
	r.toModel(s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceHarborPilotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m spaceHarborPilotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.c.GetSpaceHarborPilot(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) || (err == nil && !s.Stored) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's Harbor Pilot settings", err)
		return
	}
	r.toModel(s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceHarborPilotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m spaceHarborPilotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetSpaceHarborPilot(ctx, m.SpaceID.ValueString())
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the space's Harbor Pilot settings", err)
			return
		}
		v = cur.Version
	}
	s, ok := r.save(ctx, &m, v, &resp.Diagnostics)
	if !ok {
		return
	}
	r.toModel(s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceHarborPilotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m spaceHarborPilotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.c.GetSpaceHarborPilot(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) || (err == nil && !cur.Stored) {
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's Harbor Pilot settings", err)
		return
	}
	if err := r.c.DeleteSpaceHarborPilot(ctx, m.SpaceID.ValueString(), cur.Version); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the space's Harbor Pilot settings", err)
	}
}

func (r *spaceHarborPilotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("space_id"), req.ID)...)
}
