package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// spaceAccountsResource owns the complete set of account bindings of one space: the API replaces the whole set.
type spaceAccountsResource struct{ base }

func newSpaceAccountsResource() resource.Resource { return &spaceAccountsResource{} }

var bindingType = types.ObjectType{AttrTypes: map[string]attr.Type{"account_id": types.StringType, "default": types.BoolType, "priority": types.Int64Type}}

// defaultPoolPriority is the priority SpaceMan stores when none is given.
const defaultPoolPriority = 100

// poolPriority maps SpaceMan's 0 (never returned, but accepted as "omitted") to the default.
func poolPriority(p int64) int64 {
	if p == 0 {
		return defaultPoolPriority
	}
	return p
}

// bindingDefaults fills the omitted default (false) and priority (100) of every configured binding. The attributes
// cannot use schema Defaults: inside a set the framework rebuilds an element per default and drops its sibling's
// configured value, so `default = true` planned as false once `priority` was omitted.
type bindingDefaults struct{}

func (bindingDefaults) Description(context.Context) string {
	return "Omitted binding default is false and omitted priority is 100."
}
func (d bindingDefaults) MarkdownDescription(ctx context.Context) string { return d.Description(ctx) }

func (bindingDefaults) PlanModifySet(ctx context.Context, req planmodifier.SetRequest, resp *planmodifier.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var elems []types.Object
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &elems, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out := make([]attr.Value, 0, len(elems))
	for _, e := range elems {
		if e.IsNull() || e.IsUnknown() {
			return
		}
		attrs := e.Attributes()
		if v, ok := attrs["default"].(types.Bool); ok && v.IsNull() {
			attrs["default"] = types.BoolValue(false)
		}
		if v, ok := attrs["priority"].(types.Int64); ok && v.IsNull() {
			attrs["priority"] = types.Int64Value(defaultPoolPriority)
		}
		out = append(out, types.ObjectValueMust(bindingType.AttrTypes, attrs))
	}
	resp.PlanValue = types.SetValueMust(bindingType, out)
}

type bindingModel struct {
	AccountID types.String `tfsdk:"account_id"`
	Default   types.Bool   `tfsdk:"default"`
	Priority  types.Int64  `tfsdk:"priority"`
}

type spaceAccountsModel struct {
	ID       types.String `tfsdk:"id"`
	SpaceID  types.String `tfsdk:"space_id"`
	Bindings types.Set    `tfsdk:"binding"`
}

func (r *spaceAccountsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_space_accounts"
}

func (r *spaceAccountsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The accounts a space's runs may use (`GET/PUT /spaces/{id}/accounts`). This resource owns the space's whole binding set: " +
			"bindings added in the UI show as drift. At most one default per harness. Destroying it removes every binding.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttr("The space ID."),
			"space_id": requiredReplace("The space."),
			"binding": schema.SetNestedAttribute{Required: true, PlanModifiers: []planmodifier.Set{bindingDefaults{}}, MarkdownDescription: "Bound accounts.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"account_id": schema.StringAttribute{Required: true, MarkdownDescription: "The account (`acc_…`)."},
					"default": schema.BoolAttribute{Optional: true, Computed: true,
						MarkdownDescription: "The account the space's runs of its harness use unless a launch names another."},
					"priority": schema.Int64Attribute{Optional: true, Computed: true,
						Validators:          []validator.Int64{int64validator.Between(1, 1000)},
						MarkdownDescription: "Pool order for usage-limit failover (SpaceMan ADR-0090): when a run's account reaches its limit, the run moves to the pool's next eligible account, lower first (1-1000; default 100)."},
				}}},
		},
	}
}

func (r *spaceAccountsResource) bindings(ctx context.Context, v types.Set, d *diag.Diagnostics) []client.AccountBinding {
	var ms []bindingModel
	d.Append(v.ElementsAs(ctx, &ms, false)...)
	out := make([]client.AccountBinding, 0, len(ms))
	for _, m := range ms {
		out = append(out, client.AccountBinding{AccountID: m.AccountID.ValueString(), Default: m.Default.ValueBool(), Priority: m.Priority.ValueInt64()})
	}
	return out
}

func bindingsValue(bs []client.AccountBinding) types.Set {
	elems := make([]attr.Value, 0, len(bs))
	for _, b := range bs {
		elems = append(elems, types.ObjectValueMust(bindingType.AttrTypes, map[string]attr.Value{
			"account_id": types.StringValue(b.AccountID), "default": types.BoolValue(b.Default), "priority": types.Int64Value(poolPriority(b.Priority))}))
	}
	return types.SetValueMust(bindingType, elems)
}

func (r *spaceAccountsResource) apply(ctx context.Context, plan *spaceAccountsModel, d *diag.Diagnostics) bool {
	bs := r.bindings(ctx, plan.Bindings, d)
	if d.HasError() {
		return false
	}
	out, err := r.c.SetSpaceAccounts(ctx, plan.SpaceID.ValueString(), bs)
	if err != nil {
		addAPIError(d, "Could not set the space's accounts", err)
		return false
	}
	plan.ID = plan.SpaceID
	plan.Bindings = bindingsValue(out)
	return true
}

func (r *spaceAccountsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m spaceAccountsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.apply(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceAccountsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m spaceAccountsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	bs, err := r.c.SpaceAccounts(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's accounts", err)
		return
	}
	m.ID = m.SpaceID
	m.Bindings = bindingsValue(bs)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceAccountsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m spaceAccountsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.apply(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceAccountsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m spaceAccountsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.c.SetSpaceAccounts(ctx, m.SpaceID.ValueString(), nil); err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Could not remove the space's accounts", err)
	}
}

func (r *spaceAccountsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("space_id"), req.ID)...)
}
