package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- Telegram settings (SpaceMan ADR-0083, plan §5.2) ----

// telegramSettingsImportID is the import ID of the settings: there is one set per central.
const telegramSettingsImportID = "telegram-settings"

type telegramSettingsResource struct{ base }

func newTelegramSettingsResource() resource.Resource { return &telegramSettingsResource{} }

type telegramSettingsModel struct {
	ID                            types.String `tfsdk:"id"`
	LinkDefaultMaxRole            types.String `tfsdk:"link_default_max_role"`
	LinkAllowAdminCap             types.Bool   `tfsdk:"link_allow_admin_cap"`
	LinkSSOGrants                 types.Bool   `tfsdk:"link_sso_grants"`
	LinkReconfirmDays             types.Int64  `tfsdk:"link_reconfirm_days"`
	LinksPerUser                  types.Int64  `tfsdk:"links_per_user"`
	LinkCodeTTLSeconds            types.Int64  `tfsdk:"link_code_ttl_seconds"`
	InitDataMaxAgeSeconds         types.Int64  `tfsdk:"init_data_max_age_seconds"`
	MiniAppSessionIdleSeconds     types.Int64  `tfsdk:"miniapp_session_idle_seconds"`
	MiniAppSessionAbsoluteSeconds types.Int64  `tfsdk:"miniapp_session_absolute_seconds"`
	SendLimits                    types.Object `tfsdk:"send_limits"`
	InboundLimits                 types.Object `tfsdk:"inbound_limits"`
	NotificationDefaults          types.Object `tfsdk:"notification_defaults"`
	TopicsModeDefault             types.Bool   `tfsdk:"topics_mode_default"`
}

type tgSendLimitsModel struct {
	GlobalPerSecond types.Float64 `tfsdk:"global_per_second"`
	ChatPerSecond   types.Float64 `tfsdk:"chat_per_second"`
	ChatBurst       types.Int64   `tfsdk:"chat_burst"`
	GroupPerMinute  types.Int64   `tfsdk:"group_per_minute"`
	QueueTotal      types.Int64   `tfsdk:"queue_total"`
	QueuePerChat    types.Int64   `tfsdk:"queue_per_chat"`
}

type tgInboundLimitsModel struct {
	ActionsPerMinute  types.Int64 `tfsdk:"actions_per_minute"`
	ActionsBurst      types.Int64 `tfsdk:"actions_burst"`
	InboxItemsPerHour types.Int64 `tfsdk:"inbox_items_per_hour"`
	DownloadsPerHour  types.Int64 `tfsdk:"downloads_per_hour"`
	UpdatesPerSecond  types.Int64 `tfsdk:"updates_per_second"`
}

type tgNotificationDefaultsModel struct {
	NewActions       types.Bool   `tfsdk:"new_actions"`
	MinPriority      types.String `tfsdk:"min_priority"`
	AssignedToMe     types.Bool   `tfsdk:"assigned_to_me"`
	Escalations      types.Bool   `tfsdk:"escalations"`
	Reminders        types.String `tfsdk:"reminders"`
	Clarifications   types.Bool   `tfsdk:"clarifications"`
	OperatorMessages types.Bool   `tfsdk:"operator_messages"`
	InboxItems       types.String `tfsdk:"inbox_items"`
	TasksInReview    types.String `tfsdk:"tasks_in_review"`
	FailedRuns       types.String `tfsdk:"failed_runs"`
	Digest           types.Bool   `tfsdk:"digest"`
	DigestTime       types.String `tfsdk:"digest_time"`
	QuietHours       types.Bool   `tfsdk:"quiet_hours"`
	QuietHoursStart  types.String `tfsdk:"quiet_hours_start"`
	QuietHoursEnd    types.String `tfsdk:"quiet_hours_end"`
}

var objectAsOpts = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}

// positiveFloat requires a value above 0.
type positiveFloat struct{}

func (positiveFloat) Description(context.Context) string               { return "a number above 0" }
func (p positiveFloat) MarkdownDescription(ctx context.Context) string { return p.Description(ctx) }
func (positiveFloat) ValidateFloat64(_ context.Context, req validator.Float64Request, resp *validator.Float64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if v := req.ConfigValue.ValueFloat64(); !(v > 0) {
		resp.Diagnostics.AddAttributeError(req.Path, "Not above 0", fmt.Sprintf("%g must be above 0.", v))
	}
}

var tgClock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// The attribute builders below are all optional and computed: left out, the value is SpaceMan's own default (shown as
// it is, so it never reads as drift), and what was set and is taken out goes back to it (see ModifyPlan). The bounds are those of SpaceMan's settings API.

func tgInt(desc string, lo, hi int64) schema.Int64Attribute {
	a := optInt64(fmt.Sprintf("%s (%d to %d)", desc, lo, hi))
	a.Validators = []validator.Int64{int64validator.Between(lo, hi)}
	return a
}

// tgRate is a rate bounded above 0 and at most hi.
func tgRate(desc string, hi float64) schema.Float64Attribute {
	return schema.Float64Attribute{Optional: true, Computed: true,
		MarkdownDescription: fmt.Sprintf("%s (above 0, at most %g)", desc, hi),
		Validators:          []validator.Float64{positiveFloat{}, float64validator.AtMost(hi)},
		PlanModifiers:       []planmodifier.Float64{float64planmodifier.UseStateForUnknown()}}
}

func tgEnum(desc string, values ...string) schema.StringAttribute {
	return optString(desc, stringvalidator.OneOf(values...))
}

func tgTime(desc string) schema.StringAttribute {
	return optString(desc+" (`HH:MM`, 24 hours)", stringvalidator.RegexMatches(tgClock, "must be a time of day as HH:MM"))
}

func tgObject(desc string, attrs map[string]schema.Attribute) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Computed: true, MarkdownDescription: desc, Attributes: attrs,
		PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()}}
}

var (
	tgSendLimitsAttrs = map[string]schema.Attribute{
		"global_per_second": tgRate("Messages per second over every chat. Default 25.", 30),
		"chat_per_second":   tgRate("Messages per second to one private chat. Default 1.", 1),
		"chat_burst":        tgInt("Messages one private chat may get in a burst. Default 3.", 1, 3),
		"group_per_minute":  tgInt("Messages per minute to one group. Default 20.", 1, 20),
		"queue_total":       tgInt("Messages waiting over every chat; beyond it the least urgent are dropped. Default 5000.", 1, 50000),
		"queue_per_chat": tgInt("Messages waiting for one chat, at most `queue_total`; beyond it the chat gets one \"N updates\" notice. Default 50.",
			1, 1000),
	}
	tgInboundLimitsAttrs = map[string]schema.Attribute{
		"actions_per_minute":   tgInt("Updates (messages and button taps) one link may send per minute. Default 30.", 1, 600),
		"actions_burst":        tgInt("Updates one link may send in a burst. Default 10.", 1, 100),
		"inbox_items_per_hour": tgInt("Inbox items one link may create per hour. Default 60.", 1, 1000),
		"downloads_per_hour":   tgInt("Attachment downloads one link may make per hour. Default 50.", 1, 1000),
		"updates_per_second":   tgInt("Updates central handles per second over every chat. Default 20.", 1, 100),
	}
	tgNotificationAttrs = map[string]schema.Attribute{
		"new_actions":       optBool("New human actions in spaces the link can read. Default `true`."),
		"min_priority":      tgEnum("Only new actions of at least this priority (assignments always count): `low`, `normal`, `high` or `urgent`. Default `normal`.", "low", "normal", "high", "urgent"),
		"assigned_to_me":    optBool("An action was assigned to the user. Default `true`."),
		"escalations":       optBool("An action escalated. Default `true`."),
		"reminders":         tgEnum("Reminders of pending actions: `push`, `digest` or `off`. Default `push`.", "push", "digest", "off"),
		"clarifications":    optBool("Clarification requests and replies. Default `true`."),
		"operator_messages": optBool("Agent messages to operators. Default `true`."),
		"inbox_items":       tgEnum("New inbox items: `push`, `digest` or `off`. Default `digest`.", "push", "digest", "off"),
		"tasks_in_review":   tgEnum("Tasks entering review: `push`, `digest` or `off`. Default `digest`.", "push", "digest", "off"),
		"failed_runs":       tgEnum("Failed or lost runs: `push`, `digest` or `off`. Default `digest`.", "push", "digest", "off"),
		"digest":            optBool("Send a daily digest. Default `true`."),
		"digest_time": tgTime("When the digest is sent, a wall-clock time in each user's own time zone (a user without one gets no digest). " +
			"Default `09:00`."),
		"quiet_hours":       optBool("Send silently (no sound) during quiet hours. Default `true`."),
		"quiet_hours_start": tgTime("Quiet hours start, in the user's time zone. Default `23:00`."),
		"quiet_hours_end": tgTime("Quiet hours end, in the user's time zone; before the start they run past midnight, and it must differ from " +
			"the start. Default `08:00`."),
	}
)

func objectTypes(attrs map[string]schema.Attribute) map[string]attr.Type {
	out := make(map[string]attr.Type, len(attrs))
	for k, a := range attrs {
		out[k] = a.GetType()
	}
	return out
}

func (r *telegramSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telegram_settings"
}

func (r *telegramSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The policy and defaults of central's Telegram bot (`/telegram/settings`, SpaceMan ADR-0083): who may link, " +
			"lifetimes, send and inbound limits, and each link's notification defaults. There is one set per central. Every attribute is " +
			"optional: only the ones you set are stored, a value left out follows SpaceMan's built-in default (taking an attribute out of " +
			"the configuration puts it back to the default), and the effective values are always shown. Out-of-range values are refused at plan time. Changes reach the running bot without a restart. A user's own " +
			"notification preferences and time zone are theirs: they override these defaults and are not managed here. The bot itself " +
			"(`spaceman_telegram_bot`) is separate, and the Bot API's address is not settable here: it stays in central's configuration file. " +
			"Destroying the resource resets every value to its built-in default. Needs `system:write` and a step-up. Import with the ID `" +
			telegramSettingsImportID + "`.",
		Attributes: map[string]schema.Attribute{
			"id": idAttr("Always `" + telegramSettingsImportID + "` (the settings are a singleton)."),
			"link_default_max_role": tgEnum("The cap a new link gets: its Telegram session never has more than this role in any space, "+
				"whatever the user's grants: `viewer` or `operator`. Default `operator`.", "viewer", "operator"),
			"link_allow_admin_cap": optBool("Whether a link may be given the admin cap at all. Default `false`."),
			"link_sso_grants": optBool("Whether a link confirmed from an SSO session also carries the user's SSO-mapped grants, until that " +
				"session's absolute expiry. Default `false`."),
			"link_reconfirm_days":   tgInt("Days after which a link must be confirmed again from the web. Default 90.", 7, 365),
			"links_per_user":        tgInt("Telegram accounts one SpaceMan user may link. Default 1.", 1, 3),
			"link_code_ttl_seconds": tgInt("Lifetime of a link code, in seconds. Default 600.", 120, 1800),
			"init_data_max_age_seconds": tgInt("The oldest Mini App `initData` accepted for a session exchange, in seconds. Default 300.",
				60, 3600),
			"miniapp_session_idle_seconds": tgInt("A Mini App session ends after this long without a request, in seconds. Default 900.",
				300, 3600),
			"miniapp_session_absolute_seconds": tgInt("A Mini App session ends this long after the exchange, whatever its use, in seconds; "+
				"at least `miniapp_session_idle_seconds`. Default 3600.", 900, 43200),
			"send_limits": tgObject("Outbound rate limits and queue bounds, each at or below Telegram's published limits.", tgSendLimitsAttrs),
			"inbound_limits": tgObject("Inbound abuse limits: what linked users can make central do. Excess gets a single \"slow down\" answer.",
				tgInboundLimitsAttrs),
			"notification_defaults": tgObject("What each link notifies about until its user changes it. Digest and quiet-hour times are "+
				"wall-clock times in each user's own time zone.", tgNotificationAttrs),
			"topics_mode_default": optBool("Whether a new link starts in topics mode (a topic per space in the private chat). Default `false`."),
		},
	}
}

// ValidateConfig checks the rules that span attributes, as far as the configuration states both sides.
func (r *telegramSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m telegramSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	known := func(v interface {
		IsNull() bool
		IsUnknown() bool
	}) bool {
		return !v.IsNull() && !v.IsUnknown()
	}
	if known(m.MiniAppSessionIdleSeconds) && known(m.MiniAppSessionAbsoluteSeconds) &&
		m.MiniAppSessionAbsoluteSeconds.ValueInt64() < m.MiniAppSessionIdleSeconds.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("miniapp_session_absolute_seconds"), "Absolute lifetime shorter than the idle timeout",
			fmt.Sprintf("miniapp_session_absolute_seconds (%d) must be at least miniapp_session_idle_seconds (%d).",
				m.MiniAppSessionAbsoluteSeconds.ValueInt64(), m.MiniAppSessionIdleSeconds.ValueInt64()))
	}
	if known(m.SendLimits) {
		var sl tgSendLimitsModel
		if !m.SendLimits.As(ctx, &sl, objectAsOpts).HasError() && known(sl.QueueTotal) && known(sl.QueuePerChat) &&
			sl.QueuePerChat.ValueInt64() > sl.QueueTotal.ValueInt64() {
			resp.Diagnostics.AddAttributeError(path.Root("send_limits").AtName("queue_per_chat"), "Per-chat queue larger than the total",
				fmt.Sprintf("queue_per_chat (%d) must not exceed queue_total (%d).", sl.QueuePerChat.ValueInt64(), sl.QueueTotal.ValueInt64()))
		}
	}
	if known(m.NotificationDefaults) {
		var nd tgNotificationDefaultsModel
		if !m.NotificationDefaults.As(ctx, &nd, objectAsOpts).HasError() && known(nd.QuietHoursStart) && known(nd.QuietHoursEnd) &&
			nd.QuietHoursStart.ValueString() == nd.QuietHoursEnd.ValueString() {
			resp.Diagnostics.AddAttributeError(path.Root("notification_defaults").AtName("quiet_hours_end"), "Empty quiet hours",
				"quiet_hours_end must differ from quiet_hours_start.")
		}
	}
}

func (r *telegramSettingsResource) toModel(ctx context.Context, s *client.TelegramSettings, m *telegramSettingsModel, d *diagnostics) {
	m.ID = types.StringValue(telegramSettingsImportID)
	m.LinkDefaultMaxRole = types.StringValue(s.LinkDefaultMaxRole)
	m.LinkAllowAdminCap = types.BoolValue(s.LinkAllowAdminCap)
	m.LinkSSOGrants = types.BoolValue(s.LinkSSOGrants)
	m.LinkReconfirmDays = types.Int64Value(s.LinkReconfirmDays)
	m.LinksPerUser = types.Int64Value(s.LinksPerUser)
	m.LinkCodeTTLSeconds = types.Int64Value(s.LinkCodeTTLSeconds)
	m.InitDataMaxAgeSeconds = types.Int64Value(s.InitDataMaxAgeSeconds)
	m.MiniAppSessionIdleSeconds = types.Int64Value(s.MiniAppSessionIdleSeconds)
	m.MiniAppSessionAbsoluteSeconds = types.Int64Value(s.MiniAppSessionAbsoluteSeconds)
	m.TopicsModeDefault = types.BoolValue(s.TopicsModeDefault)
	sl := s.SendLimits
	var diags diagnostics
	m.SendLimits, diags = types.ObjectValueFrom(ctx, objectTypes(tgSendLimitsAttrs), tgSendLimitsModel{
		GlobalPerSecond: types.Float64Value(sl.GlobalPerSecond), ChatPerSecond: types.Float64Value(sl.ChatPerSecond),
		ChatBurst: types.Int64Value(sl.ChatBurst), GroupPerMinute: types.Int64Value(sl.GroupPerMinute),
		QueueTotal: types.Int64Value(sl.QueueTotal), QueuePerChat: types.Int64Value(sl.QueuePerChat)})
	d.Append(diags...)
	il := s.InboundLimits
	m.InboundLimits, diags = types.ObjectValueFrom(ctx, objectTypes(tgInboundLimitsAttrs), tgInboundLimitsModel{
		ActionsPerMinute: types.Int64Value(il.ActionsPerMinute), ActionsBurst: types.Int64Value(il.ActionsBurst),
		InboxItemsPerHour: types.Int64Value(il.InboxItemsPerHour), DownloadsPerHour: types.Int64Value(il.DownloadsPerHour),
		UpdatesPerSecond: types.Int64Value(il.UpdatesPerSecond)})
	d.Append(diags...)
	n := s.NotificationDefaults
	m.NotificationDefaults, diags = types.ObjectValueFrom(ctx, objectTypes(tgNotificationAttrs), tgNotificationDefaultsModel{
		NewActions: types.BoolValue(n.NewActions), MinPriority: types.StringValue(n.MinPriority), AssignedToMe: types.BoolValue(n.AssignedToMe),
		Escalations: types.BoolValue(n.Escalations), Reminders: types.StringValue(n.Reminders), Clarifications: types.BoolValue(n.Clarifications),
		OperatorMessages: types.BoolValue(n.OperatorMessages), InboxItems: types.StringValue(n.InboxItems),
		TasksInReview: types.StringValue(n.TasksInReview), FailedRuns: types.StringValue(n.FailedRuns), Digest: types.BoolValue(n.Digest),
		DigestTime: types.StringValue(n.DigestTime), QuietHours: types.BoolValue(n.QuietHours),
		QuietHoursStart: types.StringValue(n.QuietHoursStart), QuietHoursEnd: types.StringValue(n.QuietHoursEnd)})
	d.Append(diags...)
}

func int64Ptr(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := v.ValueInt64()
	return &x
}

func float64Ptr(v types.Float64) *float64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := v.ValueFloat64()
	return &x
}

// input is the PUT body: the model is the configuration, so a value left out is not sent.
func (r *telegramSettingsResource) input(ctx context.Context, m *telegramSettingsModel, d *diagnostics) client.TelegramSettingsInput {
	in := client.TelegramSettingsInput{
		LinkDefaultMaxRole: strPtr(m.LinkDefaultMaxRole), LinkAllowAdminCap: boolPtr(m.LinkAllowAdminCap), LinkSSOGrants: boolPtr(m.LinkSSOGrants),
		LinkReconfirmDays: int64Ptr(m.LinkReconfirmDays), LinksPerUser: int64Ptr(m.LinksPerUser), LinkCodeTTLSeconds: int64Ptr(m.LinkCodeTTLSeconds),
		InitDataMaxAgeSeconds: int64Ptr(m.InitDataMaxAgeSeconds), MiniAppSessionIdleSeconds: int64Ptr(m.MiniAppSessionIdleSeconds),
		MiniAppSessionAbsoluteSeconds: int64Ptr(m.MiniAppSessionAbsoluteSeconds), TopicsModeDefault: boolPtr(m.TopicsModeDefault),
	}
	if known := !m.SendLimits.IsNull() && !m.SendLimits.IsUnknown(); known {
		var s tgSendLimitsModel
		d.Append(m.SendLimits.As(ctx, &s, objectAsOpts)...)
		in.SendLimits = &client.TelegramSendLimitsInput{GlobalPerSecond: float64Ptr(s.GlobalPerSecond), ChatPerSecond: float64Ptr(s.ChatPerSecond),
			ChatBurst: int64Ptr(s.ChatBurst), GroupPerMinute: int64Ptr(s.GroupPerMinute), QueueTotal: int64Ptr(s.QueueTotal),
			QueuePerChat: int64Ptr(s.QueuePerChat)}
	}
	if known := !m.InboundLimits.IsNull() && !m.InboundLimits.IsUnknown(); known {
		var s tgInboundLimitsModel
		d.Append(m.InboundLimits.As(ctx, &s, objectAsOpts)...)
		in.InboundLimits = &client.TelegramInboundLimitsInput{ActionsPerMinute: int64Ptr(s.ActionsPerMinute), ActionsBurst: int64Ptr(s.ActionsBurst),
			InboxItemsPerHour: int64Ptr(s.InboxItemsPerHour), DownloadsPerHour: int64Ptr(s.DownloadsPerHour), UpdatesPerSecond: int64Ptr(s.UpdatesPerSecond)}
	}
	if known := !m.NotificationDefaults.IsNull() && !m.NotificationDefaults.IsUnknown(); known {
		var n tgNotificationDefaultsModel
		d.Append(m.NotificationDefaults.As(ctx, &n, objectAsOpts)...)
		in.NotificationDefaults = &client.TelegramNotificationDefaultsInput{NewActions: boolPtr(n.NewActions), MinPriority: strPtr(n.MinPriority),
			AssignedToMe: boolPtr(n.AssignedToMe), Escalations: boolPtr(n.Escalations), Reminders: strPtr(n.Reminders),
			Clarifications: boolPtr(n.Clarifications), OperatorMessages: boolPtr(n.OperatorMessages), InboxItems: strPtr(n.InboxItems),
			TasksInReview: strPtr(n.TasksInReview), FailedRuns: strPtr(n.FailedRuns), Digest: boolPtr(n.Digest), DigestTime: strPtr(n.DigestTime),
			QuietHours: boolPtr(n.QuietHours), QuietHoursStart: strPtr(n.QuietHoursStart), QuietHoursEnd: strPtr(n.QuietHoursEnd)}
	}
	return in
}

func (r *telegramSettingsResource) save(ctx context.Context, m *telegramSettingsModel, version int64, d *diagnostics) (*client.TelegramSettings, bool) {
	in := r.input(ctx, m, d)
	if d.HasError() {
		return nil, false
	}
	s, err := r.c.PutTelegramSettings(ctx, version, in)
	if err != nil {
		addAPIError(d, "Could not save the Telegram settings", err)
		return nil, false
	}
	return s, true
}

func (r *telegramSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Only the configured values are sent; the rest takes SpaceMan's defaults and is read back.
	var m telegramSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.c.GetTelegramSettings(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Telegram settings", err)
		return
	}
	if cur.Stored {
		resp.Diagnostics.AddError("Telegram settings are already stored",
			fmt.Sprintf("Import them with `terraform import <address> %s`, or reset them in SpaceMan first.", telegramSettingsImportID))
		return
	}
	s, ok := r.save(ctx, &m, 0, &resp.Diagnostics)
	if !ok {
		return
	}
	r.toModel(ctx, s, &m, &resp.Diagnostics)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	setConfiguredLeaves(ctx, resp.Private, req.Config, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *telegramSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m telegramSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.c.GetTelegramSettings(ctx)
	if client.IsNotFound(err) || (err == nil && !s.Stored) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Telegram settings", err)
		return
	}
	r.toModel(ctx, s, &m, &resp.Diagnostics)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *telegramSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m telegramSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetTelegramSettings(ctx)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the Telegram settings", err)
			return
		}
		v = cur.Version
	}
	s, ok := r.save(ctx, &m, v, &resp.Diagnostics)
	if !ok {
		return
	}
	r.toModel(ctx, s, &m, &resp.Diagnostics)
	setVersion(ctx, resp.Private, s.Version, &resp.Diagnostics)
	setConfiguredLeaves(ctx, resp.Private, req.Config, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *telegramSettingsResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	cur, err := r.c.GetTelegramSettings(ctx)
	if client.IsNotFound(err) || (err == nil && !cur.Stored) {
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Telegram settings", err)
		return
	}
	if err := r.c.DeleteTelegramSettings(ctx, cur.Version); err != nil {
		addAPIError(&resp.Diagnostics, "Could not reset the Telegram settings", err)
	}
}

func (r *telegramSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != telegramSettingsImportID {
		resp.Diagnostics.AddError("Unexpected import ID", fmt.Sprintf("The Telegram settings are a singleton: import them with the ID %q, not %q.",
			telegramSettingsImportID, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), telegramSettingsImportID)...)
}
