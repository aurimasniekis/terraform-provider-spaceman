package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- nodes ----

type nodeResource struct{ base }

func newNodeResource() resource.Resource { return &nodeResource{} }

type nodeResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Description         types.String `tfsdk:"description"`
	Labels              types.Map    `tfsdk:"labels"`
	Enabled             types.Bool   `tfsdk:"enabled"`
	EnrollmentVersion   types.Int64  `tfsdk:"enrollment_version"`
	EnrollmentToken     types.String `tfsdk:"enrollment_token"`
	EnrollmentExpiresAt types.String `tfsdk:"enrollment_expires_at"`
	RevokeOnDestroy     types.Bool   `tfsdk:"revoke_on_destroy"`
	State               types.String `tfsdk:"state"`
}

func (r *nodeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node"
}

func (r *nodeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	token := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	}
	tok := token("The one-time enrollment token: run `spaceman-agent enroll --token …` on the host with it before it expires. Set on create and " +
		"whenever `enrollment_version` changes; empty after an import. It is stored in the Terraform state (sensitive): it works once, and only " +
		"until `enrollment_expires_at`.")
	tok.Sensitive = true
	resp.Schema = schema.Schema{
		MarkdownDescription: "A node (`POST/GET/PUT /nodes`): a host that runs `spaceman-agent`. Creating it registers the node and returns its " +
			"one-time `enrollment_token`; pass it to the host's provisioning (cloud-init, Ansible) to enroll the agent. Destroying it revokes the " +
			"node (permanent: its credentials stop working, its spaces' runs stop, and its name stays taken) unless `revoke_on_destroy = false`, " +
			"which only removes it from the state. Connection, capacity and agent version are runtime state: read them with the `spaceman_node` " +
			"data source.",
		Attributes: map[string]schema.Attribute{
			"id":          idAttr("The node ID (`nod_…`)."),
			"name":        schema.StringAttribute{Required: true, MarkdownDescription: "Lowercase DNS-like name, unique (letters, digits, dashes; at most 63)."},
			"description": optString("Description."),
			"labels":      optMap("Labels (at most 32; keys are lowercase names, values at most 128 bytes)."),
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "`false` disables the node: it gets no new runs and its connection is closed. Default `true`. A node that has not " +
					"enrolled yet stays `pending` either way."},
			"enrollment_version": schema.Int64Attribute{Optional: true,
				MarkdownDescription: "Change this number to issue a new enrollment token, for example to rebuild the host. Using the new token " +
					"re-enrolls the node and replaces its credentials; the old token stops working."},
			"enrollment_token":      tok,
			"enrollment_expires_at": token("When `enrollment_token` expires (RFC 3339; SpaceMan's `nodes.enrollment_ttl`, 24 hours by default)."),
			"revoke_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "On destroy, revoke the node (default `true`). `false` only removes it from the Terraform state."},
			"state": computedString("`pending` (not enrolled yet), `active` or `disabled`."),
		},
	}
}

func (r *nodeResource) toModel(n *client.Node, m *nodeResourceModel) {
	m.ID = types.StringValue(n.ID)
	m.Name = types.StringValue(n.Name)
	m.Description = types.StringValue(n.Description)
	m.Labels = mapValue(n.Labels)
	m.Enabled = types.BoolValue(n.State != "disabled")
	m.State = types.StringValue(n.State)
	if m.RevokeOnDestroy.IsNull() || m.RevokeOnDestroy.IsUnknown() {
		m.RevokeOnDestroy = types.BoolValue(true)
	}
	for _, v := range []*types.String{&m.EnrollmentToken, &m.EnrollmentExpiresAt} {
		if v.IsUnknown() {
			*v = types.StringNull()
		}
	}
}

func (r *nodeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m nodeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.c.CreateNode(ctx, client.CreateNodeInput{Name: m.Name.ValueString(), Description: strOrEmpty(m.Description),
		Labels: stringMap(ctx, m.Labels, &resp.Diagnostics)})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not create the node", err)
		return
	}
	m.EnrollmentToken, m.EnrollmentExpiresAt = types.StringValue(e.Token), types.StringValue(e.ExpiresAt)
	n := e.Node
	if !m.Enabled.ValueBool() {
		if n, err = r.c.SetNodeState(ctx, n.ID, "disabled"); err != nil {
			addAPIError(&resp.Diagnostics, "Could not disable the node", err)
			return
		}
	}
	r.toModel(n, &m)
	setVersion(ctx, resp.Private, n.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *nodeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m nodeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n, err := r.c.GetNode(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the node", err)
		return
	}
	if n.State == "revoked" {
		resp.Diagnostics.AddWarning("Node revoked", fmt.Sprintf("Node %s (%s) was revoked in SpaceMan and cannot be used again. Terraform removed it "+
			"from the state. Its name stays taken: give the resource a new name to register a new node.", n.Name, n.ID))
		resp.State.RemoveResource(ctx)
		return
	}
	r.toModel(n, &m)
	setVersion(ctx, resp.Private, n.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *nodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior nodeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := prior.ID.ValueString()
	cur, err := r.c.GetNode(ctx, id)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the node", err)
		return
	}
	n := cur
	in := client.CreateNodeInput{Name: m.Name.ValueString(), Description: strOrEmpty(m.Description), Labels: stringMap(ctx, m.Labels, &resp.Diagnostics)}
	if in.Labels == nil {
		in.Labels = cur.Labels
	}
	if in.Name != cur.Name || in.Description != cur.Description || !sameLabels(in.Labels, cur.Labels) {
		v := getVersion(ctx, req.Private, &resp.Diagnostics)
		if v == 0 {
			v = cur.Version
		}
		if n, err = r.c.UpdateNode(ctx, id, v, in); err != nil {
			addAPIError(&resp.Diagnostics, "Could not update the node", err)
			return
		}
	}
	state := ""
	switch want := m.Enabled.ValueBool(); {
	case !want && n.State != "disabled":
		state = "disabled"
	case want && n.State == "disabled":
		state = "active"
	}
	if state != "" {
		if n, err = r.c.SetNodeState(ctx, id, state); err != nil {
			addAPIError(&resp.Diagnostics, "Could not change the node's state", err)
			return
		}
	}
	m.EnrollmentToken, m.EnrollmentExpiresAt = prior.EnrollmentToken, prior.EnrollmentExpiresAt
	if !m.EnrollmentVersion.Equal(prior.EnrollmentVersion) && !m.EnrollmentVersion.IsNull() {
		e, err := r.c.ReissueEnrollment(ctx, id)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not issue a new enrollment token", err)
			return
		}
		m.EnrollmentToken, m.EnrollmentExpiresAt = types.StringValue(e.Token), types.StringValue(e.ExpiresAt)
		if e.Node != nil {
			n = e.Node
		}
	}
	r.toModel(n, &m)
	setVersion(ctx, resp.Private, n.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ModifyPlan marks what an apply recomputes: the state when enabled changes, and the enrollment token when
// enrollment_version changes (a new token is issued).
func (r *nodeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state nodeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	changed := false
	if !plan.Enabled.Equal(state.Enabled) {
		plan.State, changed = types.StringUnknown(), true
	}
	if !plan.EnrollmentVersion.IsNull() && !plan.EnrollmentVersion.Equal(state.EnrollmentVersion) {
		plan.EnrollmentToken, plan.EnrollmentExpiresAt, changed = types.StringUnknown(), types.StringUnknown(), true
	}
	if changed {
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	}
}

func (r *nodeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m nodeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !m.RevokeOnDestroy.ValueBool() {
		return
	}
	n, err := r.c.GetNode(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the node", err)
		return
	}
	if n.State == "revoked" {
		return
	}
	if _, err := r.c.SetNodeState(ctx, n.ID, "revoked"); err != nil {
		addAPIError(&resp.Diagnostics, "Could not revoke the node", err)
	}
}

func (r *nodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importPassthrough(ctx, req, resp)
}

func sameLabels(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
