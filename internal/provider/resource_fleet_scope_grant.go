package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- fleet-scope grant (SpaceMan ADR-0106) ----

type fleetScopeGrantResource struct{ base }

func newFleetScopeGrantResource() resource.Resource { return &fleetScopeGrantResource{} }

type fleetScopeGrantModel struct {
	SpaceID            types.String `tfsdk:"space_id"`
	Reason             types.String `tfsdk:"reason"`
	GrantedAt          types.String `tfsdk:"granted_at"`
	GrantedBy          types.String `tfsdk:"granted_by"`
	FleetSubscriptions types.Int64  `tfsdk:"fleet_subscriptions"`
}

func (r *fleetScopeGrantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_event_fleet_scope_grant"
}

func (r *fleetScopeGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lets a space's event subscriptions use `ingest_own_scope = \"fleet\"`: ingest the run dialog events " +
			"(`evt.spaceman.fleet.run.dialog_detected` and `_cleared`, automation depth 0, identifying fields only) of **every** space " +
			"(`/event-fleet-scope-grants/{space_id}`, SpaceMan ADR-0106). This is installation administration: **the provider's token must be an " +
			"operator-level token** (`system:write`; API tokens skip step-up), a space's own permissions never suffice, and it is never reachable " +
			"through SpaceMan MCP or Telegram. Destroying the grant revokes it at once: the space's fleet-scoped subscriptions then filter other " +
			"spaces' events, and updating them is refused until their `ingest_own_scope` is back to `space`. Needs SpaceMan 0.1.17 or later.",
		Attributes: map[string]schema.Attribute{
			"space_id": requiredReplace("The space that receives the grant (not archived). Changing it replaces the grant. Also the import ID."),
			"reason": schema.StringAttribute{Optional: true, Validators: []validatorString{stringvalidator.LengthAtMost(500)},
				MarkdownDescription: "Why the space may ingest every space's run dialog events (at most 500 characters); recorded on the grant and in the audit log. Changing it replaces the reason in place."},
			"granted_at":          computedString("When the grant was first given (RFC 3339)."),
			"granted_by":          computedString("Who gave the grant."),
			"fleet_subscriptions": schema.Int64Attribute{Computed: true, MarkdownDescription: "How many of the space's subscriptions currently use `ingest_own_scope = \"fleet\"` (read-only)."},
		},
	}
}

func (r *fleetScopeGrantResource) toModel(g *client.FleetScopeGrant, m *fleetScopeGrantModel) {
	m.SpaceID = types.StringValue(g.SpaceID)
	m.GrantedAt = types.StringValue(g.GrantedAt)
	m.GrantedBy = types.StringValue(g.GrantedBy)
	m.FleetSubscriptions = types.Int64Value(g.FleetSubscriptions)
	// An unset reason stays null, not "". SpaceMan trims the reason, so a configured value that differs from the stored one
	// only in surrounding whitespace is kept: the state then matches the configuration and does not show a diff.
	if (g.Reason != "" || !m.Reason.IsNull()) && strings.TrimSpace(strOrEmpty(m.Reason)) != g.Reason {
		m.Reason = types.StringValue(g.Reason)
	}
}

// addFleetScopeAPIError reports a failed grant call with the causes this operation can have.
func addFleetScopeAPIError(d *diag.Diagnostics, summary string, err error) {
	var ae *client.APIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusConflict:
			if ae.Code != "precondition_failed" {
				break
			}
			d.AddError(summary, err.Error()+"\n\nSpaceMan refused the grant because the space is archived. Grant fleet scope to an active space, or restore the space first.")
			return
		case http.StatusForbidden, http.StatusUnauthorized:
			d.AddError(summary, err.Error()+"\n\nFleet-scope grants need an operator-level token (system:write): a space's own permissions, even admin, never suffice. "+
				"Use an API token of an installation administrator for this provider.")
			return
		}
	}
	addAPIError(d, summary, err)
}

func (r *fleetScopeGrantResource) put(ctx context.Context, m *fleetScopeGrantModel, diags *diag.Diagnostics, summary string) {
	g, err := r.c.PutFleetScopeGrant(ctx, m.SpaceID.ValueString(), client.FleetScopeInput{Reason: strings.TrimSpace(strOrEmpty(m.Reason))})
	if err != nil {
		addFleetScopeAPIError(diags, summary, err)
		return
	}
	r.toModel(g, m)
}

func (r *fleetScopeGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m fleetScopeGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.put(ctx, &m, &resp.Diagnostics, "Could not create the fleet-scope grant"); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *fleetScopeGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m fleetScopeGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.c.GetFleetScopeGrant(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addFleetScopeAPIError(&resp.Diagnostics, "Could not read the fleet-scope grant", err)
		return
	}
	r.toModel(g, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update re-puts the grant: PUT is idempotent and replaces the reason.
func (r *fleetScopeGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m fleetScopeGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.put(ctx, &m, &resp.Diagnostics, "Could not update the fleet-scope grant"); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *fleetScopeGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m fleetScopeGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteFleetScopeGrant(ctx, m.SpaceID.ValueString()); err != nil && !client.IsNotFound(err) {
		addFleetScopeAPIError(&resp.Diagnostics, "Could not revoke the fleet-scope grant", err)
	}
}

func (r *fleetScopeGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("space_id"), req, resp)
}
