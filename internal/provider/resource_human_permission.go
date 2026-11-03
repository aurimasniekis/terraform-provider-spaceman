package provider

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- human-action type permissions (SM-HUI-008, ADR-0050) ----

type humanPermissionResource struct{ base }

func newHumanTypePermissionResource() resource.Resource { return &humanPermissionResource{} }

type humanPermissionModel struct {
	ID          types.String `tfsdk:"id"`
	SpaceID     types.String `tfsdk:"space_id"`
	ActionType  types.String `tfsdk:"action_type"`
	TypeSlug    types.String `tfsdk:"type_slug"`
	ViewRole    types.String `tfsdk:"view_role"`
	CreateRole  types.String `tfsdk:"create_role"`
	RespondRole types.String `tfsdk:"respond_role"`
	Agents      types.String `tfsdk:"agents"`
	AgentRoles  types.Set    `tfsdk:"agent_roles"`
}

func (r *humanPermissionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_human_action_type_permission"
}

func (r *humanPermissionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Who may see, create and respond to one human-action type in a space (SM-HUI-008, ADR-0050; " +
			"`/spaces/{id}/human-type-permissions/{type}`). Roles are minimums over the space's built-in roles and only narrow the roles' own " +
			"permissions (`human_action:read`, `human_action:respond`, `approval:decide`): a type without a stored permission lets viewers see, " +
			"operators create and respond, and any agent ask. Changing a permission needs `human_type_permission:write` (space or global admins) " +
			"and is audited. Creating a permission that already exists fails (import it). Destroying the resource deletes the permission; the " +
			"defaults apply again. Fields left out start from the defaults and are not owned afterwards.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttr("`<space_id>/<action_type>`."),
			"space_id": requiredReplace("The space."),
			"action_type": requiredReplace("`question`, `approval`, `todo`, `message`, or a custom type's ID (`hty_…`, preferably " +
				"`spaceman_human_action_type.<name>.id`) or slug."),
			"type_slug": computedString("A custom type's slug (empty for built-in types)."),
			"view_role": optString("Weakest role that sees actions of the type: `viewer` (default), `operator` or `admin`.",
				stringvalidator.OneOf("viewer", "operator", "admin")),
			"create_role": optString("Weakest role that may ask people with the type: `operator` (default) or `admin`.",
				stringvalidator.OneOf("operator", "admin")),
			"respond_role": optString("Weakest role that may respond, assign, comment, clarify, move or cancel: `operator` (default) or `admin`.",
				stringvalidator.OneOf("operator", "admin")),
			"agents": optString("Which agents may ask with the type: `any` (default), `roles` (only `agent_roles`) or `none`.",
				stringvalidator.OneOf("any", "roles", "none")),
			"agent_roles": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Space roles whose agents may ask when `agents` is `roles` (at least one then; empty otherwise).",
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
		},
	}
}

// ModifyPlan keeps agent_roles consistent with agents: when the configuration leaves agent_roles out and agents is not
// roles, SpaceMan stores no roles, so the plan says so instead of keeping the previous roles.
func (r *humanPermissionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var cfg types.Set
	var agents types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("agent_roles"), &cfg)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("agents"), &agents)...)
	if resp.Diagnostics.HasError() || !cfg.IsNull() || agents.IsUnknown() {
		return
	}
	if agents.IsNull() || agents.ValueString() != "roles" {
		if req.State.Raw.IsNull() && agents.IsNull() {
			return // a create without agents: the server decides (any, no roles)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("agent_roles"), setValue(nil))...)
	}
}

func (r *humanPermissionResource) toModel(p *client.HumanTypePermission, m *humanPermissionModel) {
	m.ID = types.StringValue(m.SpaceID.ValueString() + "/" + m.ActionType.ValueString())
	m.TypeSlug = types.StringValue(p.TypeSlug)
	m.ViewRole = types.StringValue(p.ViewRole)
	m.CreateRole = types.StringValue(p.CreateRole)
	m.RespondRole = types.StringValue(p.RespondRole)
	m.Agents = types.StringValue(p.Agents)
	m.AgentRoles = setValue(p.AgentRoles)
}

// input is the planned permission; fields the plan does not know come from prior (nil: the defaults).
func (m *humanPermissionModel) input(ctx context.Context, prior *humanPermissionModel, d *diagnostics) client.HumanTypePermissionInput {
	pick := func(v types.String, p func(*humanPermissionModel) types.String) string {
		if s := strOrEmpty(v); s != "" || prior == nil {
			return s
		}
		return strOrEmpty(p(prior))
	}
	in := client.HumanTypePermissionInput{
		ViewRole:    pick(m.ViewRole, func(x *humanPermissionModel) types.String { return x.ViewRole }),
		CreateRole:  pick(m.CreateRole, func(x *humanPermissionModel) types.String { return x.CreateRole }),
		RespondRole: pick(m.RespondRole, func(x *humanPermissionModel) types.String { return x.RespondRole }),
		Agents:      pick(m.Agents, func(x *humanPermissionModel) types.String { return x.Agents }),
	}
	roles := m.AgentRoles
	if (roles.IsNull() || roles.IsUnknown()) && prior != nil {
		roles = prior.AgentRoles
	}
	in.AgentRoles = stringSet(ctx, roles, d)
	return in
}

func (r *humanPermissionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m humanPermissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spaceID, typ := m.SpaceID.ValueString(), m.ActionType.ValueString()
	cur, err := r.c.GetHumanTypePermission(ctx, spaceID, typ)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the type permission", err)
		return
	}
	if cur.Stored {
		resp.Diagnostics.AddError("The type permission already exists",
			"SpaceMan already stores a permission for "+typ+" in this space (version "+strconv.FormatInt(cur.Version, 10)+"). Terraform does not overwrite it: "+
				"import it with `terraform import <address> "+spaceID+"/"+typ+"`.")
		return
	}
	in := m.input(ctx, nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.c.SetHumanTypePermission(ctx, spaceID, typ, 0, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not create the type permission", err)
		return
	}
	r.toModel(p, &m)
	setVersion(ctx, resp.Private, p.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *humanPermissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m humanPermissionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.c.GetHumanTypePermission(ctx, m.SpaceID.ValueString(), m.ActionType.ValueString())
	if client.IsNotFound(err) || (err == nil && !p.Stored) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the type permission", err)
		return
	}
	r.toModel(p, &m)
	setVersion(ctx, resp.Private, p.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *humanPermissionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior humanPermissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spaceID, typ := prior.SpaceID.ValueString(), prior.ActionType.ValueString()
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetHumanTypePermission(ctx, spaceID, typ)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the type permission", err)
			return
		}
		v = cur.Version
	}
	in := m.input(ctx, &prior, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.c.SetHumanTypePermission(ctx, spaceID, typ, v, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not update the type permission", err)
		return
	}
	r.toModel(p, &m)
	setVersion(ctx, resp.Private, p.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *humanPermissionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m humanPermissionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteHumanTypePermission(ctx, m.SpaceID.ValueString(), m.ActionType.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Could not delete the type permission", err)
	}
}

// ImportState takes `<space_id>/<action_type>`.
func (r *humanPermissionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitImportID(req.ID, 2, "<space_id>/<action_type>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("space_id"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("action_type"), p[1])...)
}
