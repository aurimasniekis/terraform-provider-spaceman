package provider

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- MCP servers ----

type mcpServerResource struct{ base }

func newMCPServerResource() resource.Resource { return &mcpServerResource{} }

type mcpServerModel struct {
	ID               types.String `tfsdk:"id"`
	Slug             types.String `tfsdk:"slug"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	URL              types.String `tfsdk:"url"`
	AllowPrivate     types.Bool   `tfsdk:"allow_private"`
	Tools            types.Set    `tfsdk:"tools"`
	CallTimeoutMS    types.Int64  `tfsdk:"call_timeout_ms"`
	MaxConcurrency   types.Int64  `tfsdk:"max_concurrency"`
	MaxResultBytes   types.Int64  `tfsdk:"max_result_bytes"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	HeadersWO        types.Map    `tfsdk:"headers_wo"`
	HeadersWOVersion types.Int64  `tfsdk:"headers_wo_version"`
	HeaderNames      types.List   `tfsdk:"header_names"`
	HeadersVersion   types.Int64  `tfsdk:"headers_version"`
}

func (r *mcpServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (r *mcpServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An upstream MCP server of the gateway (`/mcp-servers`). Header values (e.g. `Authorization`) are write-only: sealed by " +
			"SpaceMan, never returned and never stored by Terraform (requires Terraform 1.11 or later). Agents see the server's tools only through " +
			"a `spaceman_mcp_grant`. Deleting the server removes its grants. SpaceMan MCP servers (SpaceMan's own API as tools) are " +
			"`spaceman_admin_mcp_server`: this resource refuses them.",
		Attributes: map[string]schema.Attribute{
			"id": idAttr("The server ID."),
			"slug": schema.StringAttribute{Required: true, MarkdownDescription: "Namespace of the server's tools (`<slug>__<tool>`). Changing it replaces the server (and drops its grants).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":          schema.StringAttribute{Required: true, MarkdownDescription: "Display name."},
			"description":   optString("Description."),
			"url":           schema.StringAttribute{Required: true, MarkdownDescription: "Streamable HTTP endpoint; `https` unless `allow_private`."},
			"allow_private": optBool("Allow loopback and private addresses and plain http (link-local stays refused). Default `false`."),
			"tools": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Upstream tools offered to agents; empty offers every tool.",
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"call_timeout_ms":  optInt64("Timeout of one tool call (server default when left out)."),
			"max_concurrency":  optInt64("Calls in flight to this server across all runs (server default when left out)."),
			"max_result_bytes": optInt64("Largest tool result passed to an agent (server default when left out)."),
			"enabled":          optBool("Whether the gateway offers the server (default `true`)."),
			"headers_wo": schema.MapAttribute{Optional: true, WriteOnly: true, Sensitive: true, ElementType: types.StringType,
				MarkdownDescription: "Request headers such as `Authorization` (write-only). Sent on create and whenever `headers_wo_version` changes; " +
					"an empty or missing map then clears the stored headers."},
			"headers_wo_version": woVersion("headers_wo"),
			"header_names": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Names of the stored headers (values are never returned).",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()}},
			"headers_version": schema.Int64Attribute{Computed: true, MarkdownDescription: "Increments whenever the stored headers change."},
		},
	}
}

func (r *mcpServerResource) toModel(_ context.Context, s *client.MCPServer, m *mcpServerModel) {
	m.ID = types.StringValue(s.ID)
	m.Slug = types.StringValue(s.Slug)
	m.Name = types.StringValue(s.Name)
	m.Description = types.StringValue(s.Description)
	m.URL = types.StringValue(s.URL)
	m.AllowPrivate = types.BoolValue(s.AllowPrivate)
	m.Tools = setValue(s.Tools)
	m.CallTimeoutMS = types.Int64Value(s.CallTimeoutMS)
	m.MaxConcurrency = types.Int64Value(s.MaxConcurrency)
	m.MaxResultBytes = types.Int64Value(s.MaxResultBytes)
	m.Enabled = types.BoolValue(s.Enabled)
	m.HeaderNames = listValue(s.HeaderNames)
	m.HeadersVersion = types.Int64Value(s.HeadersVersion)
	m.HeadersWO = types.MapNull(types.StringType)
}

func (r *mcpServerResource) input(ctx context.Context, m *mcpServerModel, diags *diagnostics) client.MCPServerInput {
	return client.MCPServerInput{Slug: m.Slug.ValueString(), Name: m.Name.ValueString(), Description: strOrEmpty(m.Description), URL: m.URL.ValueString(),
		AllowPrivate: m.AllowPrivate.ValueBool(), Tools: stringSet(ctx, m.Tools, diags), CallTimeoutMS: int64OrZero(m.CallTimeoutMS),
		MaxConcurrency: int64OrZero(m.MaxConcurrency), MaxResultBytes: int64OrZero(m.MaxResultBytes), Enabled: boolPtr(m.Enabled)}
}

func (r *mcpServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m mcpServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	var headers types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo"), &headers)...)
	in := r.input(ctx, &m, &resp.Diagnostics)
	in.Headers = stringMap(ctx, headers, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.c.CreateMCPServer(ctx, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not register the MCP server", err)
		return
	}
	r.toModel(ctx, s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mcpServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m mcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.c.GetMCPServer(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the MCP server", err)
		return
	}
	if s.Kind == client.MCPKindSpaceman {
		resp.Diagnostics.AddError("Not an external MCP server",
			"MCP server "+s.ID+" ("+s.Slug+") is a SpaceMan MCP server: manage it with spaceman_admin_mcp_server, not spaceman_mcp_server.")
		return
	}
	r.toModel(ctx, s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mcpServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior mcpServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	in := r.input(ctx, &m, &resp.Diagnostics)
	if in.Tools == nil {
		in.Tools = []string{}
	}
	if !m.HeadersWOVersion.Equal(prior.HeadersWOVersion) {
		var headers types.Map
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo"), &headers)...)
		in.SetHeaders = true
		in.Headers = stringMap(ctx, headers, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetMCPServer(ctx, prior.ID.ValueString())
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the MCP server", err)
			return
		}
		v = cur.Version
	}
	s, err := r.c.UpdateMCPServer(ctx, prior.ID.ValueString(), v, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not update the MCP server", err)
		return
	}
	r.toModel(ctx, s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mcpServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m mcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteMCPServer(ctx, m.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the MCP server", err)
	}
}

func (r *mcpServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importPassthrough(ctx, req, resp)
}

// ---- MCP grants ----

type mcpGrantResource struct{ base }

func newMCPGrantResource() resource.Resource { return &mcpGrantResource{} }

type mcpGrantModel struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	SpaceID  types.String `tfsdk:"space_id"`
	Role     types.String `tfsdk:"role"`
	Tools    types.Set    `tfsdk:"tools"`
}

func (r *mcpGrantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_grant"
}

func (r *mcpGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Grants an MCP server to a space role (`POST/DELETE /mcp-servers/{id}/grants`). Grants cannot be edited: a change of " +
			"`tools`, the server or the space revokes the grant and adds a new one, and a revoked grant refuses the affected runs' next call. " +
			"A change of `role` is applied in place (see `role`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The grant ID. Stays when `role` follows a rename of the space role; a new grant has a new ID.",
				PlanModifiers: []planmodifier.String{useStateUnlessChanges{attr: "role"}}},
			"server_id": requiredReplace("The MCP server."),
			"space_id":  requiredReplace("The space."),
			"role": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("*"),
				MarkdownDescription: "A space role name, or `*` (default) for every role. Renaming the `spaceman_space_role` this references " +
					"changes nothing in SpaceMan: the rename moves the grant with the role, the grant keeps its ID and no run is without it. " +
					"Pointing `role` at a different role revokes the grant and adds one for the new role."},
			"tools": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Subset of the server's tools; empty grants all of them.",
				PlanModifiers:       []planmodifier.Set{setplanmodifier.RequiresReplace(), setplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *mcpGrantResource) toModel(_ context.Context, g *client.MCPGrant, m *mcpGrantModel) {
	m.ID = types.StringValue(g.ID)
	m.ServerID = types.StringValue(g.ServerID)
	m.SpaceID = types.StringValue(g.SpaceID)
	m.Role = types.StringValue(g.Role)
	m.Tools = setValue(g.Tools)
}

func (r *mcpGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m mcpGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	tools := stringSet(ctx, m.Tools, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.c.AddMCPGrant(ctx, m.ServerID.ValueString(), client.MCPGrantInput{SpaceID: m.SpaceID.ValueString(), Role: m.Role.ValueString(), Tools: tools})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not grant the MCP server", err)
		return
	}
	r.toModel(ctx, g, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mcpGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m mcpGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.c.GetMCPGrant(ctx, m.ServerID.ValueString(), m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the MCP grant", err)
		return
	}
	r.toModel(ctx, g, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update applies a change of `role` (the only attribute that is not a replacement). When central already rewrote the grant
// to the planned role, in the transaction that renamed the space role, nothing is left to do: the grant keeps its ID and
// is never without its role. Otherwise the grant points at a different role: revoke it and add one for the new role.
func (r *mcpGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, old mcpGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	tools := stringSet(ctx, m.Tools, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.c.GetMCPGrant(ctx, old.ServerID.ValueString(), old.ID.ValueString())
	if err == nil && g.Role == m.Role.ValueString() {
		r.toModel(ctx, g, &m)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	if err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Could not read the MCP grant", err)
		return
	}
	oldExists := err == nil
	// Add the new grant before revoking the old one: the role is never without a grant.
	g, err = r.c.AddMCPGrant(ctx, m.ServerID.ValueString(), client.MCPGrantInput{SpaceID: m.SpaceID.ValueString(), Role: m.Role.ValueString(), Tools: tools})
	if err != nil && isAlreadyExists(err) {
		// A grant for the role already exists (an earlier apply added it and could not revoke the old one): adopt it
		// when it has the planned tools, otherwise say what is in the way.
		ex, ferr := r.c.FindMCPGrantByRole(ctx, m.ServerID.ValueString(), m.SpaceID.ValueString(), m.Role.ValueString())
		switch {
		case ferr != nil:
			resp.Diagnostics.AddError("Could not grant the MCP server",
				"a grant for role "+m.Role.ValueString()+" on this server already exists, and looking it up failed: "+ferr.Error()+"; import it or revoke it")
			return
		case sameStrings(ex.Tools, tools):
			g, err = ex, nil
		default:
			resp.Diagnostics.AddError("Could not grant the MCP server",
				"a grant "+ex.ID+" for role "+m.Role.ValueString()+" on this server already exists with tools ["+strings.Join(ex.Tools, ", ")+
					"], not the planned ["+strings.Join(tools, ", ")+"]; import it or revoke it")
			return
		}
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not grant the MCP server", err)
		return
	}
	// The new grant exists: save it before anything else can fail, so state never keeps pointing at the old one.
	r.toModel(ctx, g, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if oldExists && old.ID.ValueString() != g.ID {
		if err := r.c.RevokeMCPGrant(ctx, old.ServerID.ValueString(), old.ID.ValueString()); err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Could not revoke the old MCP grant",
				"The grant for role "+m.Role.ValueString()+" was added and is saved in state, but the old grant "+old.ID.ValueString()+
					" (role "+old.Role.ValueString()+") could not be revoked: "+err.Error()+
					". Revoke it (DELETE /mcp-servers/"+old.ServerID.ValueString()+"/grants/"+old.ID.ValueString()+"); it is no longer tracked by Terraform.")
		}
	}
}

// isAlreadyExists reports whether err is a 409 already_exists problem.
func isAlreadyExists(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && ae.Status == http.StatusConflict && ae.Code == "already_exists"
}

// sameStrings reports whether a and b hold the same strings, ignoring order.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string{}, a...)
	y := append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func (r *mcpGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m mcpGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RevokeMCPGrant(ctx, m.ServerID.ValueString(), m.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Could not revoke the MCP grant", err)
	}
}

func (r *mcpGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := splitImportID(req.ID, 2, "<server_id>/<grant_id>", &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), p[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), p[1])...)
}
