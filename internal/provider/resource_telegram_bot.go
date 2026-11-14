package provider

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// ---- Telegram bot (SpaceMan ADR-0083) ----

// telegramBotImportID is the import ID of the bot: there is one per central.
const telegramBotImportID = "telegram-bot"

type telegramBotResource struct{ base }

func newTelegramBotResource() resource.Resource { return &telegramBotResource{} }

type telegramBotModel struct {
	ID                 types.String `tfsdk:"id"`
	BotTokenWO         types.String `tfsdk:"bot_token_wo"`
	BotTokenWOVersion  types.Int64  `tfsdk:"bot_token_wo_version"`
	Enabled            types.Bool   `tfsdk:"enabled"`
	Mode               types.String `tfsdk:"mode"`
	MiniAppHost        types.String `tfsdk:"miniapp_host"`
	WebhookIPAllowlist types.Set    `tfsdk:"webhook_ip_allowlist"`
	BotID              types.Int64  `tfsdk:"bot_id"`
	Username           types.String `tfsdk:"username"`
	HasToken           types.Bool   `tfsdk:"has_token"`
	TokenVersion       types.Int64  `tfsdk:"token_version"`
	WebhookStatus      types.String `tfsdk:"webhook_status"`
}

// canonicalCIDR accepts only CIDRs spelled the way SpaceMan stores them (masked, canonical text), so the value read
// back always equals the configured one.
type canonicalCIDR struct{}

func (canonicalCIDR) Description(context.Context) string {
	return "a CIDR in canonical form, e.g. `149.154.160.0/20`"
}
func (c canonicalCIDR) MarkdownDescription(ctx context.Context) string { return c.Description(ctx) }
func (canonicalCIDR) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	p, err := netip.ParsePrefix(req.ConfigValue.ValueString())
	if err != nil || p.Masked() != p || p.String() != req.ConfigValue.ValueString() {
		resp.Diagnostics.AddAttributeError(req.Path, "Not a canonical CIDR",
			fmt.Sprintf("%q must be a CIDR in canonical form (host bits zero, e.g. 149.154.160.0/20).", req.ConfigValue.ValueString()))
	}
}

func (r *telegramBotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telegram_bot"
}

func (r *telegramBotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computedInt := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: desc,
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Central's Telegram bot (`/telegram/bot`, SpaceMan ADR-0083). There is one per central. Linked users get notifications " +
			"and can act on approvals from Telegram; the Mini App opens SpaceMan inside Telegram.\n\n" +
			"The bot token is write-only: checked with Telegram, sealed by SpaceMan, never returned and never stored by Terraform (requires " +
			"Terraform 1.11 or later). Configuring a different bot (another bot ID) revokes every Telegram link; a new token of the same " +
			"bot keeps them. The Bot API's address is not settable here: it stays in central's configuration file. Destroying the resource " +
			"removes the webhook and the stored token, so Telegram is off; links stay and resume if the same bot is configured again. " +
			"Needs `system:write` and a step-up. Import with the ID `" + telegramBotImportID + "`.",
		Attributes: map[string]schema.Attribute{
			"id": idAttr("Always `" + telegramBotImportID + "` (the bot is a singleton)."),
			"bot_token_wo": schema.StringAttribute{Required: true, WriteOnly: true, Sensitive: true,
				MarkdownDescription: "The token BotFather gave (`123456:ABC…`) (write-only). Sent on create and whenever " +
					"`bot_token_wo_version` changes; otherwise SpaceMan keeps the stored token."},
			"bot_token_wo_version": woVersion("bot_token_wo"),
			"enabled":              optBool("Whether central runs the bot (webhook or polling). `false` (the default): Telegram is off and the webhook is removed."),
			"mode": optString("How updates arrive: `webhook` (default; needs central's public URL to be https) or `polling` (outbound only).",
				stringvalidator.OneOf("webhook", "polling")),
			"miniapp_host": optString("The Mini App's own host: a lower-case DNS name such as `tg.central.example.com`, without scheme, port or " +
				"path, not an IP address and not the host of central's public URL. Empty (the default): the Mini App is off."),
			"webhook_ip_allowlist": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				MarkdownDescription: "CIDRs (canonical form, e.g. `149.154.160.0/20`) the webhook accepts requests from, at most 32. Empty (the " +
					"default): no source check. Only safe when the client address behind your proxy is trustworthy.",
				Validators:    []validator.Set{setvalidator.SizeAtMost(32), setvalidator.ValueStringsAre(canonicalCIDR{})},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()}},
			"bot_id": computedInt("The bot's Telegram user ID, from Telegram's `getMe`."),
			"username": schema.StringAttribute{Computed: true, MarkdownDescription: "The bot's username (without `@`), from Telegram's `getMe`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"has_token":     schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether SpaceMan holds a bot token."},
			"token_version": computedInt("SpaceMan's counter of bot token writes."),
			"webhook_status": schema.StringAttribute{Computed: true, MarkdownDescription: "Live health of the bot's update delivery: `off`, `starting`, " +
				"`polling`, `ok`, `error` or `unknown`. It changes without Terraform."},
		},
	}
}

func (r *telegramBotResource) toModel(b *client.TelegramBot, m *telegramBotModel) {
	m.ID = types.StringValue(telegramBotImportID)
	m.BotTokenWO = types.StringNull()
	m.Enabled = types.BoolValue(b.Enabled)
	m.Mode = types.StringValue(b.Mode)
	m.MiniAppHost = types.StringValue(b.MiniAppHost)
	m.WebhookIPAllowlist = setValue(b.WebhookIPAllowlist)
	m.BotID = types.Int64Value(b.BotID)
	m.Username = types.StringValue(b.Username)
	m.HasToken = types.BoolValue(b.HasToken)
	m.TokenVersion = types.Int64Value(b.TokenVersion)
	m.WebhookStatus = types.StringValue(b.WebhookStatus)
}

func (r *telegramBotResource) input(ctx context.Context, m *telegramBotModel, d *diagnostics) client.TelegramBotInput {
	allow := stringSet(ctx, m.WebhookIPAllowlist, d)
	if allow == nil {
		allow = []string{}
	}
	return client.TelegramBotInput{Enabled: m.Enabled.ValueBool(), Mode: strOrEmpty(m.Mode), MiniAppHost: strOrEmpty(m.MiniAppHost),
		WebhookIPAllowlist: allow}
}

// configToken reads the write-only token from the configuration (it is never in the plan or the state).
func (r *telegramBotResource) configToken(ctx context.Context, cfg tfsdk.Config, d *diagnostics) string {
	var t types.String
	d.Append(cfg.GetAttribute(ctx, path.Root("bot_token_wo"), &t)...)
	return strOrEmpty(t)
}

func (r *telegramBotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m telegramBotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	token := r.configToken(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	in := r.input(ctx, &m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	in.BotToken = token
	b, err := r.c.PutTelegramBot(ctx, 0, in)
	if err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("A Telegram bot is already configured",
				fmt.Sprintf("%v\n\nSpaceMan has one bot per central. Import it with `terraform import <address> %s`.", err, telegramBotImportID))
			return
		}
		addAPIError(&resp.Diagnostics, "Could not configure the Telegram bot", err)
		return
	}
	addWarnings(&resp.Diagnostics, "Telegram bot", b.Warnings)
	r.toModel(b, &m)
	setVersion(ctx, resp.Private, b.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *telegramBotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m telegramBotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b, err := r.c.GetTelegramBot(ctx)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Telegram bot", err)
		return
	}
	r.toModel(b, &m)
	setVersion(ctx, resp.Private, b.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ModifyPlan keeps the token facts from the state when an update sends no token: only a token write changes them.
func (r *telegramBotResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, prior telegramBotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.BotTokenWOVersion.Equal(prior.BotTokenWOVersion) {
		// A token write may switch the bot or bump the counter: unknown until applied.
		plan.BotID, plan.Username, plan.HasToken, plan.TokenVersion =
			types.Int64Unknown(), types.StringUnknown(), types.BoolUnknown(), types.Int64Unknown()
	} else {
		plan.HasToken = prior.HasToken
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *telegramBotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior telegramBotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	token := r.configToken(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	in := r.input(ctx, &m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.BotTokenWOVersion.Equal(prior.BotTokenWOVersion) {
		if token == "" {
			resp.Diagnostics.AddAttributeError(path.Root("bot_token_wo"), "No bot token to send",
				"bot_token_wo_version changed, so the token is sent again, but bot_token_wo is empty.")
			return
		}
		in.BotToken = token
	}
	v := getVersion(ctx, req.Private, &resp.Diagnostics)
	if v == 0 {
		cur, err := r.c.GetTelegramBot(ctx)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Could not read the Telegram bot", err)
			return
		}
		v = cur.Version
	}
	b, err := r.c.PutTelegramBot(ctx, v, in)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Could not update the Telegram bot", err)
		return
	}
	addWarnings(&resp.Diagnostics, "Telegram bot", b.Warnings)
	r.toModel(b, &m)
	setVersion(ctx, resp.Private, b.Version, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *telegramBotResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	cur, err := r.c.GetTelegramBot(ctx)
	if client.IsNotFound(err) {
		return
	} else if err != nil {
		addAPIError(&resp.Diagnostics, "Could not read the Telegram bot", err)
		return
	}
	if err := r.c.DeleteTelegramBot(ctx, cur.Version); err != nil {
		addAPIError(&resp.Diagnostics, "Could not remove the Telegram bot", err)
	}
}

func (r *telegramBotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != telegramBotImportID {
		resp.Diagnostics.AddError("Unexpected import ID", fmt.Sprintf("The Telegram bot is a singleton: import it with the ID %q, not %q.", telegramBotImportID, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), telegramBotImportID)...)
}
