package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// spaceRolePoolResource owns one role's account pool of a space (`PUT /spaces/{id}/account-pools/{role}`).
type spaceRolePoolResource struct{ base }

func newSpaceRolePoolResource() resource.Resource { return &spaceRolePoolResource{} }

var poolAccountType = types.ObjectType{AttrTypes: map[string]attr.Type{"account_id": types.StringType, "priority": types.Int64Type}}

type poolAccountModel struct {
	AccountID types.String `tfsdk:"account_id"`
	Priority  types.Int64  `tfsdk:"priority"`
}

type spaceRolePoolModel struct {
	ID       types.String `tfsdk:"id"`
	SpaceID  types.String `tfsdk:"space_id"`
	Role     types.String `tfsdk:"role"`
	Accounts types.Set    `tfsdk:"account"`
}

func (r *spaceRolePoolResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_space_role_pool"
}

func (r *spaceRolePoolResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The accounts one role of a space may use when a run reaches a usage limit (`PUT /spaces/{id}/account-pools/{role}`, " +
			"SpaceMan ADR-0090). A role with a pool uses only these accounts instead of the space's pool; each must also be bound to the space " +
			"(`spaceman_space_accounts`). The resource owns the role's whole pool, so entries added in the UI show as drift. Destroying it removes " +
			"the role's pool, and the role uses the space's pool again. Needs a SpaceMan release with account pools (after 0.1.0-rc.8).",
		Attributes: map[string]schema.Attribute{
			"id":       idAttr("`<space_id>/<role>`."),
			"space_id": requiredReplace("The space."),
			"role":     requiredReplace("The role name (the space role's `name`)."),
			"account": schema.SetNestedAttribute{Required: true, Validators: []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.SizeAtMost(50)},
				MarkdownDescription: "The pool's accounts.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"account_id": schema.StringAttribute{Required: true, MarkdownDescription: "The account (`acc_…`)."},
					"priority": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(defaultPoolPriority),
						Validators:          []validator.Int64{int64validator.Between(1, 1000)},
						MarkdownDescription: "Order within the pool, lower first (1-1000; default 100)."},
				}}},
		},
	}
}

func poolEntries(ctx context.Context, v types.Set, d *diag.Diagnostics) []client.PoolEntry {
	var ms []poolAccountModel
	d.Append(v.ElementsAs(ctx, &ms, false)...)
	out := make([]client.PoolEntry, 0, len(ms))
	for _, m := range ms {
		out = append(out, client.PoolEntry{AccountID: m.AccountID.ValueString(), Priority: m.Priority.ValueInt64()})
	}
	return out
}

func poolValue(es []client.PoolEntry) types.Set {
	elems := make([]attr.Value, 0, len(es))
	for _, e := range es {
		elems = append(elems, types.ObjectValueMust(poolAccountType.AttrTypes, map[string]attr.Value{
			"account_id": types.StringValue(e.AccountID), "priority": types.Int64Value(poolPriority(e.Priority))}))
	}
	return types.SetValueMust(poolAccountType, elems)
}

// rolePoolOf finds a role's pool; ok is false when the role has none.
func rolePoolOf(pools []client.RolePool, role string) ([]client.PoolEntry, bool) {
	for _, p := range pools {
		if p.Role == role && len(p.Items) > 0 {
			return p.Items, true
		}
	}
	return nil, false
}

// put writes the pool. The If-Match version is read right before the write: the same apply may also change the
// space's bindings, which bumps the version of the shared account configuration.
func (r *spaceRolePoolResource) put(ctx context.Context, spaceID, role string, entries []client.PoolEntry) ([]client.PoolEntry, error) {
	_, version, err := r.c.RolePools(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	pools, _, err := r.c.SetRolePool(ctx, spaceID, role, version, entries)
	if err != nil {
		return nil, err
	}
	got, _ := rolePoolOf(pools, role)
	return got, nil
}

func (r *spaceRolePoolResource) apply(ctx context.Context, m *spaceRolePoolModel, d *diag.Diagnostics) bool {
	es := poolEntries(ctx, m.Accounts, d)
	if d.HasError() {
		return false
	}
	got, err := r.put(ctx, m.SpaceID.ValueString(), m.Role.ValueString(), es)
	if err != nil {
		addAPIError(d, "Could not set the role's account pool", err)
		return false
	}
	m.ID = types.StringValue(m.SpaceID.ValueString() + "/" + m.Role.ValueString())
	m.Accounts = poolValue(got)
	return true
}

func (r *spaceRolePoolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m spaceRolePoolModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.apply(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceRolePoolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m spaceRolePoolModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pools, _, err := r.c.RolePools(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's account pools", err)
		return
	}
	es, ok := rolePoolOf(pools, m.Role.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	m.ID = types.StringValue(m.SpaceID.ValueString() + "/" + m.Role.ValueString())
	m.Accounts = poolValue(es)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceRolePoolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m spaceRolePoolModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.apply(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceRolePoolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m spaceRolePoolModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.put(ctx, m.SpaceID.ValueString(), m.Role.ValueString(), nil); err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Could not remove the role's account pool", err)
	}
}

func (r *spaceRolePoolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitImportID(req.ID, 2, "<space_id>/<role>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &spaceRolePoolModel{ID: types.StringValue(req.ID), SpaceID: types.StringValue(p[0]),
		Role: types.StringValue(p[1]), Accounts: types.SetNull(poolAccountType)})...)
}
