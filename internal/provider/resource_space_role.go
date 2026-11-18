package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func requiredReplace(desc string) schema.StringAttribute {
	return schema.StringAttribute{Required: true, MarkdownDescription: desc,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
}

func optMap(desc string) schema.MapAttribute {
	return schema.MapAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: desc,
		PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()}}
}

func optList(desc string) schema.ListAttribute {
	return schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: desc,
		PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()}}
}

func skillsAttr(desc string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{Optional: true, Computed: true, MarkdownDescription: desc,
		PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"skill_id": schema.StringAttribute{Required: true, MarkdownDescription: "The skill ID (`skl_…`)."},
			"pin": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "A published version ID; empty follows the latest version.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		}}}
}

type spaceRoleResource struct{ base }

func newSpaceRoleResource() resource.Resource { return &spaceRoleResource{} }

type spaceRoleModel struct {
	ID           types.String `tfsdk:"id"`
	SpaceID      types.String `tfsdk:"space_id"`
	Name         types.String `tfsdk:"name"`
	TemplateID   types.String `tfsdk:"template_id"`
	ProfileID    types.String `tfsdk:"profile_id"`
	PromptID     types.String `tfsdk:"prompt_id"`
	PromptPin    types.String `tfsdk:"prompt_pin"`
	Model        types.String `tfsdk:"model"`
	Settings     types.Map    `tfsdk:"settings"`
	Instructions types.String `tfsdk:"instructions"`
	Description  types.String `tfsdk:"description"`
	Skills       types.List   `tfsdk:"skills"`
	Revision     types.Int64  `tfsdk:"revision"`
}

func (r *spaceRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_space_role"
}

func (r *spaceRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A role configured in a space (`PUT/DELETE /spaces/{id}/roles/{name}`): a role template with space-specific overrides. " +
			"Non-empty fields override the template's; changes apply to later launches.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "`<space_id>/<name>`; changes when the role is renamed.",
				PlanModifiers: []planmodifier.String{useStateUnlessChanges{attr: "name"}}},
			"space_id": requiredReplace("The space."),
			"name": schema.StringAttribute{Required: true, MarkdownDescription: "Role name, lowercase (e.g. `implementor-backend`). Changing it renames the role in place " +
				"(`POST /spaces/{id}/roles/{name}/rename`, SpaceMan 0.1.11 or later): live references to the role in the space follow it, and " +
				"a role with live work is refused. Never a destroy and create. A `spaceman_mcp_grant` and a `spaceman_space_role_pool` that " +
				"reference the role are updated in place and keep their ids.",
				Validators: []validator.String{stringvalidator.RegexMatches(roleNameRE, "must match ^[a-z][a-z0-9-]{0,47}$")}},
			"template_id":  schema.StringAttribute{Required: true, MarkdownDescription: "The role template (`rtp_…`)."},
			"profile_id":   optString("Harness profile overriding the template's."),
			"prompt_id":    optString("Prompt overriding the template's."),
			"prompt_pin":   optString("A published version of `prompt_id`; empty follows the latest version."),
			"model":        optString("Model overriding the profile's."),
			"settings":     optMap("Harness settings overriding the template's."),
			"instructions": optString("Space-specific text appended after the role's prompt."),
			"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringDefault(""),
				MarkdownDescription: "What the role is for in this space; empty uses the role template's description. Changed in place (SpaceMan 0.1.11 or later)."},
			"skills":   skillsAttr("Skills added to the template's; a pin here wins."),
			"revision": schema.Int64Attribute{Computed: true, MarkdownDescription: "Increments on every change of the role."},
		},
	}
}

// roleSeenKey is the private-state key holding the role's `updated_at` (RFC 3339, as a JSON string) as of the last
// Create, Update, Read or import. A rename is followed only if it is later: an older one belongs to a role that held the
// name before.
const roleSeenKey = "role_updated_at"

func setRoleSeen(ctx context.Context, p privateWriter, sr *client.SpaceRole, d *diag.Diagnostics) {
	if p == nil || sr.UpdatedAt.IsZero() {
		return
	}
	b, _ := json.Marshal(sr.UpdatedAt.UTC().Format(time.RFC3339Nano))
	d.Append(p.SetKey(ctx, roleSeenKey, b)...)
}

// getRoleSeen returns the stored `updated_at`; zero when none is stored.
func getRoleSeen(ctx context.Context, p privateReader, d *diag.Diagnostics) time.Time {
	if p == nil {
		return time.Time{}
	}
	b, diags := p.GetKey(ctx, roleSeenKey)
	d.Append(diags...)
	var s string
	if len(b) == 0 || json.Unmarshal(b, &s) != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func (r *spaceRoleResource) toModel(sr *client.SpaceRole, m *spaceRoleModel) {
	m.ID = types.StringValue(sr.SpaceID + "/" + sr.Name)
	m.SpaceID = types.StringValue(sr.SpaceID)
	m.Name = types.StringValue(sr.Name)
	m.TemplateID = types.StringValue(sr.TemplateID)
	m.ProfileID = types.StringValue(sr.ProfileID)
	m.PromptID = types.StringValue(sr.PromptID)
	m.PromptPin = types.StringValue(sr.PromptPin)
	m.Model = types.StringValue(sr.Model)
	m.Settings = mapValue(sr.Settings)
	m.Instructions = types.StringValue(sr.Instructions)
	m.Description = types.StringValue(sr.Description)
	m.Skills = skillRefsValue(sr.Skills, m.Skills)
	m.Revision = types.Int64Value(sr.Revision)
}

func (r *spaceRoleResource) input(ctx context.Context, m *spaceRoleModel, d *diag.Diagnostics) client.SpaceRoleInput {
	return client.SpaceRoleInput{TemplateID: m.TemplateID.ValueString(), ProfileID: strOrEmpty(m.ProfileID), PromptID: strOrEmpty(m.PromptID),
		PromptPin: strOrEmpty(m.PromptPin), Model: strOrEmpty(m.Model), Settings: stringMap(ctx, m.Settings, d), Instructions: strOrEmpty(m.Instructions),
		Description: strOrEmpty(m.Description), Skills: skillRefs(ctx, m.Skills, d)}
}

func (r *spaceRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m spaceRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The API's save is an upsert: refuse to take over a role someone else configured (import it instead).
	if _, err := r.c.GetSpaceRole(ctx, m.SpaceID.ValueString(), m.Name.ValueString()); err == nil {
		resp.Diagnostics.AddError("The space role already exists",
			"Role "+m.Name.ValueString()+" is already configured in space "+m.SpaceID.ValueString()+". Import it with "+
				"`terraform import spaceman_space_role.<name> "+m.SpaceID.ValueString()+"/"+m.Name.ValueString()+"` to manage it.")
		return
	} else if !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Could not read the space's roles", err)
		return
	}
	in := r.input(ctx, &m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	sr, err := r.c.SaveSpaceRole(ctx, m.SpaceID.ValueString(), m.Name.ValueString(), in)
	if err != nil {
		r.saveError(&resp.Diagnostics, "Could not configure the space role", err, &m)
		return
	}
	r.toModel(sr, &m)
	setRoleSeen(ctx, resp.Private, sr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m spaceRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sr, err := r.c.GetSpaceRole(ctx, m.SpaceID.ValueString(), m.Name.ValueString())
	if client.IsNotFound(err) {
		// Renamed outside Terraform: follow the rename so the role shows as an in-place `name` change back to the
		// configured value, not as a resource to create. An older SpaceMan has no rename history: the role is gone. Only a
		// rename made after the role was last seen counts: the name may belong to a new role since an older rename.
		renames, lerr := r.c.ListSpaceRoleRenames(ctx, m.SpaceID.ValueString())
		if lerr != nil && !client.IsNotFound(lerr) {
			addAPIError(&resp.Diagnostics, "Could not read the space's role renames", lerr)
			return
		}
		if now := client.FollowSpaceRoleRename(renames, m.Name.ValueString(), getRoleSeen(ctx, req.Private, &resp.Diagnostics)); lerr == nil && now != m.Name.ValueString() {
			sr, err = r.c.GetSpaceRole(ctx, m.SpaceID.ValueString(), now)
		}
	}
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space role", err)
		return
	}
	r.toModel(sr, &m)
	setRoleSeen(ctx, resp.Private, sr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m spaceRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var old spaceRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := r.input(ctx, &m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	space := m.SpaceID.ValueString()
	if old.Name.ValueString() != m.Name.ValueString() {
		renamed, err := r.c.RenameSpaceRole(ctx, space, old.Name.ValueString(), m.Name.ValueString())
		if err != nil {
			r.renameError(ctx, &resp.Diagnostics, space, old.Name.ValueString(), m.Name.ValueString(), err)
			return
		}
		setRoleSeen(ctx, resp.Private, &renamed.SpaceRole, &resp.Diagnostics)
		// The role now carries the new name even if the save below fails: record it so the next plan sees the truth.
		old.Name, old.ID = m.Name, types.StringValue(space+"/"+m.Name.ValueString())
		resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
	}
	sr, err := r.c.SaveSpaceRole(ctx, space, m.Name.ValueString(), in)
	if err != nil {
		r.saveError(&resp.Diagnostics, "Could not update the space role", err, &m)
		return
	}
	r.toModel(sr, &m)
	setRoleSeen(ctx, resp.Private, sr, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m spaceRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteSpaceRole(ctx, m.SpaceID.ValueString(), m.Name.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the space role", err)
	}
}

func (r *spaceRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitImportID(req.ID, 2, "<space_id>/<role_name>", &resp.Diagnostics)
	if p == nil {
		return
	}
	// The baseline of the rename guard: Read follows a rename of this name only if it happened after the role the
	// import found. A name that does not exist yet has no baseline, so nothing is followed.
	if sr, err := r.c.GetSpaceRole(ctx, p[0], p[1]); err == nil {
		setRoleSeen(ctx, resp.Private, sr, &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &spaceRoleModel{ID: types.StringValue(req.ID), SpaceID: types.StringValue(p[0]), Name: types.StringValue(p[1]),
		TemplateID: types.StringNull(), ProfileID: types.StringNull(), PromptID: types.StringNull(), PromptPin: types.StringNull(), Model: types.StringNull(),
		Settings: types.MapNull(types.StringType), Instructions: types.StringNull(), Description: types.StringNull(), Skills: types.ListNull(skillRefType), Revision: types.Int64Null()})...)
}

// saveError reports a failed save; an older SpaceMan (before 0.1.11) rejects `description` with a 422.
func (r *spaceRoleResource) saveError(diags *diag.Diagnostics, summary string, err error, m *spaceRoleModel) {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Status == 422 && m.Description.ValueString() != "" {
		err = fmt.Errorf("%w\n\n`description` on a space role needs SpaceMan 0.1.11 or later", err)
	}
	addAPIError(diags, summary, err)
}

// renameError reports a failed rename. A 404/405 on the action path of a role that exists means a SpaceMan without the
// endpoint; a 409 carries the blockers (role_busy), a taken name or a node on an older protocol in its detail.
func (r *spaceRoleResource) renameError(ctx context.Context, d *diag.Diagnostics, space, from, to string, err error) {
	var ae *client.APIError
	if errors.As(err, &ae) && (ae.Status == http.StatusMethodNotAllowed || ae.Status == http.StatusNotFound) {
		if _, gerr := r.c.GetSpaceRole(ctx, space, from); gerr == nil {
			d.AddError("Renaming a role needs SpaceMan 0.1.11 or later",
				"This SpaceMan has no endpoint to rename role "+from+" (\""+ae.Operation+"\" returned "+http.StatusText(ae.Status)+"). Upgrade "+
					"SpaceMan, or change `name` back: the provider never deletes and recreates a role to rename it.")
			return
		}
	}
	if errors.As(err, &ae) && ae.Status == http.StatusConflict {
		// Central words the reason into the detail (role_busy, node_too_old); a taken name is its own code.
		hint := "SpaceMan refused the rename; the role was not changed."
		switch {
		case strings.Contains(ae.Detail, "role_busy"):
			hint = "A role with live work cannot be renamed. Stop or finish the work named above, then apply again; nothing that references the role was changed."
		case strings.Contains(ae.Detail, "node_too_old"):
			hint = "A node of this space speaks an older protocol and cannot follow the rename. Upgrade the node, then apply again."
		case ae.Code == "already_exists" || ae.Code == "name_taken" || strings.Contains(ae.Detail, "name_taken"):
			hint = "The new name is already used by a role or a memory collection of the space. Pick another name."
		}
		d.AddError("Could not rename the space role "+from+" to "+to, err.Error()+"\n\n"+hint)
		return
	}
	addAPIError(d, "Could not rename the space role "+from+" to "+to, err)
}

// useStateUnlessChanges keeps the state's id, except when the attribute it is derived from changes: the id is then
// unknown until the change is applied (a role's id is `<space_id>/<name>`, a pool's `<space_id>/<role>`).
type useStateUnlessChanges struct{ attr string }

func (m useStateUnlessChanges) Description(context.Context) string {
	return "Keeps the id unless `" + m.attr + "` changes."
}

func (m useStateUnlessChanges) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m useStateUnlessChanges) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	var planVal, stateVal types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root(m.attr), &planVal)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root(m.attr), &stateVal)...)
	if resp.Diagnostics.HasError() || (!planVal.IsUnknown() && planVal.Equal(stateVal)) {
		resp.PlanValue = req.StateValue
	}
}
