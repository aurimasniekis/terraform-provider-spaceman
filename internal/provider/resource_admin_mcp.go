package provider

import (
	"context"
	"regexp"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- SpaceMan MCP servers (kind spaceman, SpaceMan ADR-0063) ----

type adminMCPServerResource struct{ base }

func newAdminMCPServerResource() resource.Resource { return &adminMCPServerResource{} }

var (
	_ resource.ResourceWithModifyPlan     = (*adminMCPServerResource)(nil)
	_ resource.ResourceWithValidateConfig = (*adminMCPServerResource)(nil)
)

type adminMCPServerModel struct {
	ID             types.String `tfsdk:"id"`
	Slug           types.String `tfsdk:"slug"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Grants         types.Set    `tfsdk:"grants"`
	Preset         types.String `tfsdk:"preset"`
	Tools          types.Set    `tfsdk:"tools"`
	Approval       types.String `tfsdk:"approval"`
	ExpiresInDays  types.Int64  `tfsdk:"expires_in_days"`
	NeverExpires   types.Bool   `tfsdk:"never_expires"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	CallTimeoutMS  types.Int64  `tfsdk:"call_timeout_ms"`
	MaxConcurrency types.Int64  `tfsdk:"max_concurrency"`
	MaxResultBytes types.Int64  `tfsdk:"max_result_bytes"`
	TokenID        types.String `tfsdk:"token_id"`
	OwnerUserID    types.String `tfsdk:"owner_user_id"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	TokenState     types.String `tfsdk:"token_state"`
	EffectiveTools types.Set    `tfsdk:"effective_tools"`
	Version        types.Int64  `tfsdk:"version"`
}

// reAdminMCPSlug is SpaceMan's slug rule for MCP servers (internal/gateway reSlug).
var reAdminMCPSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func (r *adminMCPServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_admin_mcp_server"
}

func (r *adminMCPServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A SpaceMan MCP server (`POST /mcp-servers/spaceman`): SpaceMan's own API offered as MCP tools to a space's control and " +
			"worker runs. It acts through an internal API token that SpaceMan mints for it, owned by the user whose API token the provider uses, " +
			"with `grants` covered by that user's own. The token has no secret, so nothing here is sensitive and nothing secret is stored in the " +
			"state. Runs see the tools only through a `spaceman_mcp_grant` (space and role).\n\n" +
			"The provider must use an API token issued by a user (not the root token, and not a token issued by another token): SpaceMan refuses " +
			"otherwise. Changing `grants` or the expiry re-issues the internal token in place (SpaceMan revokes the old one and mints a new " +
			"`token_id` in one step); every other change is an in-place edit. Deleting the server revokes its token and removes its grants. " +
			"Dangerous tools (deletes, secrets, profile hooks and the like) always need a person's approval in SpaceMan, whatever `approval` says. " +
			"Use `spaceman_mcp_server` for external (http) MCP servers; each resource refuses the other kind.\n\n" +
			"Reading the server (every refresh and plan) lists its effective tools through SpaceMan's `check-mcp-server`, which needs " +
			"`mcp:write`: the provider's token needs that permission (for example `admin` on `*`) even to plan.",
		Attributes: map[string]schema.Attribute{
			"id": idAttr("The server ID."),
			"slug": schema.StringAttribute{Required: true,
				MarkdownDescription: "Namespace of the server's tools (`<slug>__<tool>`): 1-31 lowercase letters, digits or dashes, starting with a letter. " +
					"`spaceman` is reserved (it is the built-in agent server). Changing it replaces the server (and drops its grants).",
				Validators: []validator.String{
					stringvalidator.RegexMatches(reAdminMCPSlug, "must be 1-31 lowercase letters, digits or dashes and start with a letter"),
					stringvalidator.NoneOf("spaceman"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":        schema.StringAttribute{Required: true, MarkdownDescription: "Display name (also the internal token's name)."},
			"description": optString("Description."),
			"grants": schema.SetNestedAttribute{Optional: true, Computed: true,
				MarkdownDescription: "The internal token's role grants, each covered by the provider user's own. Left out at create, SpaceMan grants `admin` " +
					"on `*` (only with the `admin` preset; other presets need grants). Changing them re-issues the token (new `token_id`). " +
					"Removing them from an existing server is refused at plan time (it would neither widen the token to `admin` on `*` nor " +
					"silently keep the old grants): set them explicitly. A server whose grants are already exactly `admin` on `*` may " +
					"leave them out.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"role": schema.StringAttribute{Required: true, MarkdownDescription: "`viewer`, `operator` or `admin`.",
						Validators: []validator.String{stringvalidator.OneOf("viewer", "operator", "admin")}},
					"domain": schema.StringAttribute{Required: true, MarkdownDescription: "A space ID, or `*` for every space and global administration."},
				}},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"preset": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("admin"),
				MarkdownDescription: "Tool set: `read`, `operate` (read and write), `admin` (read, write and dangerous; the default) or `custom` (only " +
					"the tools named in `tools`).",
				Validators: []validator.String{stringvalidator.OneOf("read", "operate", "admin", "custom")}},
			"tools": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: setdefault.StaticValue(types.SetValueMust(types.StringType, nil)),
				MarkdownDescription: "Tools added to the preset by name, without the slug (for example `delete_task`). Dangerous and sensitive-read " +
					"tools are offered only by name or by the `admin` preset."},
			"approval": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("none"),
				MarkdownDescription: "`none` (the default): only dangerous tools need a person's approval; `writes`: every write tool does too. " +
					"Dangerous tools are always gated by SpaceMan.",
				Validators: []validator.String{stringvalidator.OneOf("none", "writes")}},
			"expires_in_days": schema.Int64Attribute{Optional: true,
				MarkdownDescription: "Lifetime of the internal token in days (1-366), counted from when it is minted. Left out, the token never " +
					"expires (the default). Changing it re-issues the token; change it again (or re-apply with a new value) to renew an expired one.",
				Validators: []validator.Int64{int64validator.Between(1, 366)}},
			"never_expires": schema.BoolAttribute{Optional: true, Computed: true,
				MarkdownDescription: "Whether the internal token never expires: `true` exactly when `expires_in_days` is left out (setting it is " +
					"optional and only documents the choice)."},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the gateway offers the server's tools (default `true`)."},
			"call_timeout_ms":  optInt64("Timeout of one tool call, 1000-20000 (server default when left out)."),
			"max_concurrency":  optInt64("Calls in flight to this server across all runs, 1-32 (server default when left out)."),
			"max_result_bytes": optInt64("Largest tool result passed to an agent, 1 KiB-4 MiB (server default when left out)."),
			"token_id":         schema.StringAttribute{Computed: true, MarkdownDescription: "The internal API token the server acts through (no secret exists for it)."},
			"owner_user_id":    schema.StringAttribute{Computed: true, MarkdownDescription: "The user whose authority the token carries: the provider's user at the last (re-)issue."},
			"expires_at":       schema.StringAttribute{Computed: true, MarkdownDescription: "When the token expires (RFC 3339); empty when it never does."},
			"token_state": schema.StringAttribute{Computed: true,
				MarkdownDescription: "`active`, or why the server's tools are refused (`revoked`, `expired`, `owner_disabled`): re-issue it by " +
					"changing `grants` or `expires_in_days`."},
			"effective_tools": schema.SetAttribute{Computed: true, ElementType: types.StringType,
				MarkdownDescription: "The tools the server offers runs (without the slug), as SpaceMan resolves the preset, `tools` and the token's " +
					"grants. Empty when the token can no longer be used."},
			"version": schema.Int64Attribute{Computed: true, MarkdownDescription: "The server's version (increments with every change)."},
		},
	}
}

func (r *adminMCPServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m adminMCPServerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.NeverExpires.IsNull() && !m.NeverExpires.IsUnknown() && !m.ExpiresInDays.IsUnknown() {
		if m.NeverExpires.ValueBool() && !m.ExpiresInDays.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("never_expires"), "Conflicting expiry", "never_expires = true cannot be combined with expires_in_days.")
		}
		if !m.NeverExpires.ValueBool() && m.ExpiresInDays.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("never_expires"), "Missing expiry", "never_expires = false needs expires_in_days.")
		}
	}
	if m.Grants.IsNull() && !m.Preset.IsNull() && !m.Preset.IsUnknown() && m.Preset.ValueString() != "admin" {
		resp.Diagnostics.AddAttributeError(path.Root("grants"), "Missing grants",
			"The "+m.Preset.ValueString()+" preset needs grants (for example viewer or operator on one space); only the admin preset defaults to admin on *.")
	}
}

// reissues reports whether the plan changes the internal token (grants or expiry): SpaceMan then re-issues it.
func (r *adminMCPServerResource) reissues(plan, prior *adminMCPServerModel) bool {
	if !plan.Grants.IsUnknown() && !plan.Grants.Equal(prior.Grants) {
		return true
	}
	// The token's expiry kind differs from the configuration (an out-of-band re-issue, or an imported expiring server
	// configured without expires_in_days): re-issue it as configured.
	if !plan.NeverExpires.IsUnknown() && !plan.NeverExpires.Equal(prior.NeverExpires) {
		return true
	}
	if plan.ExpiresInDays.Equal(prior.ExpiresInDays) {
		return false
	}
	// An imported server has no expires_in_days in the state (SpaceMan returns only expires_at): adopt the configured
	// value for a token that expires, without re-issuing it.
	imported := prior.ExpiresInDays.IsNull() && !prior.NeverExpires.ValueBool()
	return !imported || plan.ExpiresInDays.IsNull()
}

// ModifyPlan derives never_expires from expires_in_days and keeps the computed token facts and effective tools from the
// state when the change does not touch them, so plans show only what really changes.
func (r *adminMCPServerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan adminMCPServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.ExpiresInDays.IsUnknown() {
		plan.NeverExpires = types.BoolValue(plan.ExpiresInDays.IsNull())
	}
	if !req.State.Raw.IsNull() {
		var prior adminMCPServerModel
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		// Removing grants must never widen the token to SpaceMan's create default (admin on *), and silently keeping
		// the old ones would hide the change: refuse it unless the server already has exactly the default.
		var cfgGrants types.Set
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("grants"), &cfgGrants)...)
		if cfgGrants.IsNull() && !prior.Grants.IsNull() && !prior.Grants.IsUnknown() && !prior.Grants.Equal(adminMCPDefaultGrants()) {
			resp.Diagnostics.AddAttributeError(path.Root("grants"), "Grants cannot be removed",
				"grants can't be removed from an existing server; set them explicitly (the server's token has other grants than "+
					"the create default, admin on *).")
		}
		if resp.Diagnostics.HasError() {
			return
		}
		reissue := r.reissues(&plan, &prior)
		switch {
		case reissue:
			// Set them unknown explicitly: when only the token's expiry kind drifted, the configuration equals the
			// state and Terraform would otherwise plan the old values.
			plan.TokenID, plan.OwnerUserID, plan.ExpiresAt, plan.TokenState = types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
			plan.EffectiveTools, plan.Version = types.SetUnknown(types.StringType), types.Int64Unknown()
		default:
			plan.TokenID, plan.OwnerUserID, plan.ExpiresAt, plan.TokenState = prior.TokenID, prior.OwnerUserID, prior.ExpiresAt, prior.TokenState
			if plan.Preset.Equal(prior.Preset) && plan.Tools.Equal(prior.Tools) {
				plan.EffectiveTools = prior.EffectiveTools
			}
			if adminMCPUnchanged(&plan, &prior) {
				plan.Version = prior.Version
			}
		}
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

// adminMCPDefaultGrants is SpaceMan's grant for a server created without grants (admin preset only): admin on *.
func adminMCPDefaultGrants() types.Set {
	return grantsValue([]client.GrantInput{{Role: "admin", Domain: "*"}})
}

// adminMCPUnchanged reports whether the plan sends nothing: every configurable attribute equals the state.
func adminMCPUnchanged(plan, prior *adminMCPServerModel) bool {
	return plan.Name.Equal(prior.Name) && plan.Description.Equal(prior.Description) && plan.Grants.Equal(prior.Grants) &&
		plan.Preset.Equal(prior.Preset) && plan.Tools.Equal(prior.Tools) && plan.Approval.Equal(prior.Approval) &&
		plan.ExpiresInDays.Equal(prior.ExpiresInDays) && plan.Enabled.Equal(prior.Enabled) && plan.CallTimeoutMS.Equal(prior.CallTimeoutMS) &&
		plan.MaxConcurrency.Equal(prior.MaxConcurrency) && plan.MaxResultBytes.Equal(prior.MaxResultBytes)
}

// toModel copies a server into the model; expires_in_days stays as configured (SpaceMan returns only expires_at).
func (r *adminMCPServerResource) toModel(s *client.SpacemanMCPServer, chk *client.MCPServerCheck, m *adminMCPServerModel) {
	info := s.Spaceman
	if info == nil {
		info = &client.SpacemanMCPInfo{}
	}
	m.ID = types.StringValue(s.ID)
	m.Slug = types.StringValue(s.Slug)
	m.Name = types.StringValue(s.Name)
	m.Description = types.StringValue(s.Description)
	m.Grants = grantsValue(info.Grants)
	m.Preset = types.StringValue(info.Preset)
	m.Tools = setValue(info.Tools)
	m.Approval = types.StringValue(info.Approval)
	m.NeverExpires = types.BoolValue(info.NeverExpires)
	m.Enabled = types.BoolValue(s.Enabled)
	m.CallTimeoutMS = types.Int64Value(s.CallTimeoutMS)
	m.MaxConcurrency = types.Int64Value(s.MaxConcurrency)
	m.MaxResultBytes = types.Int64Value(s.MaxResultBytes)
	m.TokenID = types.StringValue(info.TokenID)
	m.OwnerUserID = types.StringValue(info.OwnerUserID)
	m.ExpiresAt = types.StringValue("")
	if info.ExpiresAt != nil {
		m.ExpiresAt = types.StringValue(info.ExpiresAt.UTC().Format(time.RFC3339))
	}
	m.TokenState = types.StringValue(info.TokenState)
	tools := []string{}
	if chk != nil {
		for _, t := range chk.Tools {
			tools = append(tools, t.Name)
		}
	}
	slices.Sort(tools)
	m.EffectiveTools = setValue(tools)
	m.Version = types.Int64Value(s.Version)
}

// read fetches a server with its effective tools, refusing a server of another kind.
func (r *adminMCPServerResource) read(ctx context.Context, id string, m *adminMCPServerModel, d *diagnostics) (found bool) {
	s, err := r.c.GetSpacemanMCPServer(ctx, id)
	if client.IsNotFound(err) {
		return false
	} else if err != nil {
		addAPIError(d, "Could not read the SpaceMan MCP server", err)
		return true
	}
	if s.Kind != client.MCPKindSpaceman {
		kind := "an external (" + s.Kind + ")"
		if s.Kind == "" {
			kind = "an older or external"
		}
		d.AddError("Not a SpaceMan MCP server",
			"MCP server "+id+" ("+s.Slug+") is "+kind+" server: manage it with spaceman_mcp_server, not spaceman_admin_mcp_server.")
		return true
	}
	chk, err := r.c.CheckMCPServer(ctx, id)
	if err != nil {
		addAPIError(d, "Could not list the SpaceMan MCP server's tools", err)
		return true
	}
	r.toModel(s, chk, m)
	return true
}

func (r *adminMCPServerResource) grants(ctx context.Context, v types.Set, d *diagnostics) []client.GrantInput {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return (&userGrantsResource{}).grants(ctx, v, d)
}

func (r *adminMCPServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m adminMCPServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	in := client.SpacemanMCPInput{Slug: m.Slug.ValueString(), Name: m.Name.ValueString(), Description: strOrEmpty(m.Description),
		Preset: m.Preset.ValueString(), Tools: stringSet(ctx, m.Tools, &resp.Diagnostics), Approval: m.Approval.ValueString(),
		Grants: r.grants(ctx, m.Grants, &resp.Diagnostics), ExpiresInDays: int64OrZero(m.ExpiresInDays), CallTimeoutMS: int64OrZero(m.CallTimeoutMS),
		MaxConcurrency: int64OrZero(m.MaxConcurrency), MaxResultBytes: int64OrZero(m.MaxResultBytes), Enabled: boolPtr(m.Enabled)}
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.c.CreateSpacemanMCPServer(ctx, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not create the SpaceMan MCP server", err)
		return
	}
	r.finish(ctx, s.ID, &m, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// finish re-reads the server after a write (for its effective tools), keeping expires_in_days as planned.
func (r *adminMCPServerResource) finish(ctx context.Context, id string, m *adminMCPServerModel, d *diagnostics) {
	days := m.ExpiresInDays
	if !r.read(ctx, id, m, d) {
		d.AddError("The SpaceMan MCP server disappeared", "MCP server "+id+" was not found right after it was written.")
	}
	m.ExpiresInDays = days
}

func (r *adminMCPServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m adminMCPServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.read(ctx, m.ID.ValueString(), &m, &resp.Diagnostics) {
		resp.State.RemoveResource(ctx)
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *adminMCPServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior adminMCPServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, version := prior.ID.ValueString(), prior.Version.ValueInt64()
	// The token first: re-issuing needs the preset in force (only admin may leave grants to SpaceMan's default), and
	// the provider always sends the grants it holds.
	if r.reissues(&m, &prior) {
		tok := client.SpacemanMCPToken{Grants: r.grants(ctx, m.Grants, &resp.Diagnostics), ExpiresInDays: int64OrZero(m.ExpiresInDays)}
		if tok.Grants == nil {
			tok.Grants = r.grants(ctx, prior.Grants, &resp.Diagnostics)
		}
		if resp.Diagnostics.HasError() {
			return
		}
		s, err := r.c.ReissueSpacemanMCPServer(ctx, id, version, tok)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not re-issue the SpaceMan MCP server's token", err)
			return
		}
		version = s.Version
	}
	in := client.SpacemanMCPUpdate{Name: m.Name.ValueString(), Description: strOrEmpty(m.Description), Preset: m.Preset.ValueString(),
		Tools: stringSet(ctx, m.Tools, &resp.Diagnostics), Approval: m.Approval.ValueString(), CallTimeoutMS: int64OrZero(m.CallTimeoutMS),
		MaxConcurrency: int64OrZero(m.MaxConcurrency), MaxResultBytes: int64OrZero(m.MaxResultBytes), Enabled: m.Enabled.ValueBool()}
	if resp.Diagnostics.HasError() {
		return
	}
	if r.settingsChanged(&m, &prior) {
		if _, err := r.c.UpdateSpacemanMCPServer(ctx, id, version, in); err != nil {
			addAPIError(&resp.Diagnostics, "Could not update the SpaceMan MCP server", err)
			return
		}
	}
	r.finish(ctx, id, &m, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// settingsChanged reports whether the plan edits the server's settings or limits (PUT /mcp-servers/{id}/spaceman).
func (r *adminMCPServerResource) settingsChanged(plan, prior *adminMCPServerModel) bool {
	return !plan.Name.Equal(prior.Name) || !plan.Description.Equal(prior.Description) || !plan.Preset.Equal(prior.Preset) ||
		!plan.Tools.Equal(prior.Tools) || !plan.Approval.Equal(prior.Approval) || !plan.Enabled.Equal(prior.Enabled) ||
		!plan.CallTimeoutMS.Equal(prior.CallTimeoutMS) || !plan.MaxConcurrency.Equal(prior.MaxConcurrency) ||
		!plan.MaxResultBytes.Equal(prior.MaxResultBytes)
}

func (r *adminMCPServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m adminMCPServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteMCPServer(ctx, m.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the SpaceMan MCP server", err)
	}
}

func (r *adminMCPServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// SpaceMan does not return expires_in_days: it stays unset, and a configured value is adopted without a re-issue.
	importPassthrough(ctx, req, resp)
}
