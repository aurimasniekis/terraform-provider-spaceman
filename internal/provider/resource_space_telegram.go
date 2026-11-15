package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- a space's Telegram settings (SpaceMan ADR-0083) ----

type spaceTelegramResource struct{ base }

func newSpaceTelegramResource() resource.Resource { return &spaceTelegramResource{} }

type spaceTelegramModel struct {
	ID                types.String `tfsdk:"id"`
	SpaceID           types.String `tfsdk:"space_id"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	ContentLevel      types.String `tfsdk:"content_level"`
	EnforcedApprovals types.String `tfsdk:"enforced_approvals"`
	Topics            types.Bool   `tfsdk:"topics"`
	TerminalWatch     types.Bool   `tfsdk:"terminal_watch"`
}

func (r *spaceTelegramResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_space_telegram"
}

func (r *spaceTelegramResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	enum := func(desc, def string, values ...string) schema.StringAttribute {
		return schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: desc, Default: stringDefault(def),
			Validators: []validator.String{stringvalidator.OneOf(values...)}}
	}
	flag := func(desc string) schema.BoolAttribute {
		return schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: desc, Default: booldefault.StaticBool(false)}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A space's Telegram settings (`/spaces/{id}/telegram`, SpaceMan ADR-0083). Telegram is opt-in per space: a space " +
			"without these settings, or with `enabled = false`, is invisible to the bot and the Mini App. The resource describes the whole " +
			"setting: an attribute left out takes its default. Needs `space_config:write` in the space and a step-up. Destroying the resource " +
			"removes the settings, so Telegram is off for the space again. Import with the space ID.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttr("The space ID."),
			"space_id": requiredReplace("The space."),
			"enabled":  schema.BoolAttribute{Required: true, MarkdownDescription: "Whether the space is visible through Telegram."},
			"content_level": enum("How much of the space Telegram shows: `full` (bodies in chat; default), `summary` (bodies only in the "+
				"Mini App) or `off` (the space stays hidden, even when `enabled`).", "full", "full", "summary", "off"),
			"enforced_approvals": enum("Whether approvals that are enforced can be decided in chat with a second confirmation (`confirm`), "+
				"or only on the web (`web_only`; default).", "web_only", "web_only", "confirm"),
			"topics":         flag("Whether the space gets its own topic for users in topics mode. Default `false`."),
			"terminal_watch": flag("Whether a run's terminal can be watched (read-only) from the Mini App. Default `false`."),
		},
	}
}

func (r *spaceTelegramResource) toModel(s *client.SpaceTelegram, m *spaceTelegramModel) {
	m.ID = types.StringValue(s.SpaceID)
	m.SpaceID = types.StringValue(s.SpaceID)
	m.Enabled = types.BoolValue(s.Enabled)
	m.ContentLevel = types.StringValue(s.ContentLevel)
	m.EnforcedApprovals = types.StringValue(s.EnforcedApprovals)
	m.Topics = types.BoolValue(s.Topics)
	m.TerminalWatch = types.BoolValue(s.TerminalWatch)
}

func (r *spaceTelegramResource) save(ctx context.Context, m *spaceTelegramModel, version int64, d *diagnostics) (*client.SpaceTelegram, bool) {
	s, err := r.c.PutSpaceTelegram(ctx, m.SpaceID.ValueString(), version, client.SpaceTelegramInput{
		Enabled: boolPtr(m.Enabled), ContentLevel: strPtr(m.ContentLevel), EnforcedApprovals: strPtr(m.EnforcedApprovals),
		Topics: boolPtr(m.Topics), TerminalWatch: boolPtr(m.TerminalWatch)})
	if err != nil {
		addAPIError(d, "Could not save the space's Telegram settings", err)
		return nil, false
	}
	return s, true
}

func (r *spaceTelegramResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m spaceTelegramModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.c.GetSpaceTelegram(ctx, m.SpaceID.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's Telegram settings", err)
		return
	}
	if cur.Stored {
		resp.Diagnostics.AddError("The space already has Telegram settings",
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

func (r *spaceTelegramResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m spaceTelegramModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.c.GetSpaceTelegram(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) || (err == nil && !s.Stored) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's Telegram settings", err)
		return
	}
	r.toModel(s, &m)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *spaceTelegramResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m spaceTelegramModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetSpaceTelegram(ctx, m.SpaceID.ValueString())
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the space's Telegram settings", err)
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

func (r *spaceTelegramResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m spaceTelegramModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.c.GetSpaceTelegram(ctx, m.SpaceID.ValueString())
	if client.IsNotFound(err) || (err == nil && !cur.Stored) {
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the space's Telegram settings", err)
		return
	}
	if err := r.c.DeleteSpaceTelegram(ctx, m.SpaceID.ValueString(), cur.Version); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the space's Telegram settings", err)
	}
}

func (r *spaceTelegramResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("space_id"), req.ID)...)
}
