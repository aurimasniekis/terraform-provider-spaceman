package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- shared Git SSH keys ----

type gitKeyResource struct{ base }

func newGitKeyResource() resource.Resource { return &gitKeyResource{} }

type gitKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	AllSpaces   types.Bool   `tfsdk:"all_spaces"`
	Spaces      types.Set    `tfsdk:"spaces"`
	KeyVersion  types.Int64  `tfsdk:"key_version"`
	PublicKey   types.String `tfsdk:"public_key"`
	Fingerprint types.String `tfsdk:"fingerprint"`
}

func (r *gitKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_git_ssh_key"
}

func (r *gitKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A shared Git SSH key (`/git-ssh-keys`, SpaceMan ADR-0058). SpaceMan generates it and keeps the private half: " +
			"neither Terraform nor agents ever see it. Add `public_key` to the Git account the listed spaces may clone with (a read-only " +
			"machine user is safest: a key on a person's account can also push), then point repositories at it with " +
			"`spaceman_space_repository.git_key_id`. Destroying it fails while a repository uses it.",
		Attributes: map[string]schema.Attribute{
			"id":          idAttr("The key ID (`gky_…`)."),
			"name":        schema.StringAttribute{Required: true, MarkdownDescription: "Unique name: lowercase letters, digits, `.`, `_` or `-`."},
			"description": optString("Description."),
			"all_spaces": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Let every space use the key. Default `false`: only `spaces`."},
			"spaces": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "IDs of the spaces that may use the key (ignored with `all_spaces`).",
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"key_version": schema.Int64Attribute{Optional: true,
				MarkdownDescription: "Change this number to rotate the key: SpaceMan replaces it at once, so update the account's SSH key in the same apply."},
			"public_key":  computedString("The public half (authorized_keys format), for the Git account's SSH key or a deploy key."),
			"fingerprint": computedString("The key's SHA256 fingerprint."),
		},
	}
}

func (r *gitKeyResource) toModel(k *client.GitKey, m *gitKeyModel) {
	m.ID = types.StringValue(k.ID)
	m.Name = types.StringValue(k.Name)
	m.Description = types.StringValue(k.Description)
	m.AllSpaces = types.BoolValue(k.AllSpaces)
	m.Spaces = setValue(k.Spaces)
	m.PublicKey = types.StringValue(k.PublicKey)
	m.Fingerprint = types.StringValue(k.Fingerprint)
}

func (r *gitKeyResource) input(ctx context.Context, m *gitKeyModel, diags *diag.Diagnostics) client.GitKeyInput {
	return client.GitKeyInput{Name: m.Name.ValueString(), Description: strOrEmpty(m.Description), AllSpaces: m.AllSpaces.ValueBool(),
		Spaces: stringSet(ctx, m.Spaces, diags)}
}

func (r *gitKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m gitKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.c.CreateGitKey(ctx, r.input(ctx, &m, &resp.Diagnostics))
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not create the Git SSH key", err)
		return
	}
	r.toModel(k, &m)
	setVersion(ctx, resp.Private, k.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *gitKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m gitKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.c.GetGitKey(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Git SSH key", err)
		return
	}
	r.toModel(k, &m)
	setVersion(ctx, resp.Private, k.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *gitKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior gitKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := prior.ID.ValueString()
	cur, err := r.c.GetGitKey(ctx, id)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Git SSH key", err)
		return
	}
	k, err := r.c.UpdateGitKey(ctx, id, cur.Version, r.input(ctx, &m, &resp.Diagnostics))
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not update the Git SSH key", err)
		return
	}
	if !m.KeyVersion.IsNull() && !m.KeyVersion.Equal(prior.KeyVersion) {
		if k, err = r.c.RotateGitKey(ctx, id, k.Version); err != nil {
			addAPIError(&resp.Diagnostics, "Could not rotate the Git SSH key", err)
			return
		}
	}
	r.toModel(k, &m)
	setVersion(ctx, resp.Private, k.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ModifyPlan marks the public half unknown when key_version changes (a rotation).
func (r *gitKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state gitKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.KeyVersion.IsNull() || plan.KeyVersion.Equal(state.KeyVersion) {
		return
	}
	plan.PublicKey, plan.Fingerprint = types.StringUnknown(), types.StringUnknown()
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *gitKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m gitKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteGitKey(ctx, m.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Could not delete the Git SSH key", err)
	}
}

func (r *gitKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importPassthrough(ctx, req, resp)
}
