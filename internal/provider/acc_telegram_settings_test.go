package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// TestAccTelegramSettings (SpaceMan ADR-0083; needs SpaceMan with the Telegram settings operations): the singleton
// starts from the built-in defaults, changes in place (including the nested limits and notification defaults), puts
// values left out of the configuration back to the defaults, imports, and resets to the defaults on destroy. Out-of-range values fail at
// plan time.
func TestAccTelegramSettings(t *testing.T) {
	const addr = "spaceman_telegram_settings.this"
	gone := func(*terraform.State) error {
		s, err := acc.client.GetTelegramSettings(context.Background())
		if err != nil {
			return err
		}
		if s.Stored || s.LinkReconfirmDays != 90 || s.SendLimits.QueueTotal != 5000 {
			return fmt.Errorf("the settings were not reset by destroy: %+v", s)
		}
		return nil
	}
	first := `
resource "spaceman_telegram_settings" "this" {
  link_default_max_role = "viewer"
  link_reconfirm_days   = 30
}
`
	changed := `
resource "spaceman_telegram_settings" "this" {
  link_default_max_role            = "operator"
  link_allow_admin_cap             = true
  link_sso_grants                  = true
  link_reconfirm_days              = 60
  links_per_user                   = 2
  link_code_ttl_seconds            = 300
  init_data_max_age_seconds        = 120
  miniapp_session_idle_seconds     = 600
  miniapp_session_absolute_seconds = 7200
  topics_mode_default              = true

  send_limits = {
    global_per_second = 12.5
    chat_per_second   = 0.5
    chat_burst        = 2
    group_per_minute  = 10
    queue_total       = 2000
    queue_per_chat    = 40
  }
  inbound_limits = {
    actions_per_minute   = 20
    actions_burst        = 5
    inbox_items_per_hour = 30
    downloads_per_hour   = 25
    updates_per_second   = 10
  }
  notification_defaults = {
    new_actions       = false
    min_priority      = "high"
    assigned_to_me    = true
    escalations       = false
    reminders         = "digest"
    clarifications    = false
    operator_messages = false
    inbox_items       = "push"
    tasks_in_review   = "off"
    failed_runs       = "push"
    digest            = false
    digest_time       = "07:30"
    quiet_hours       = false
    quiet_hours_start = "22:00"
    quiet_hours_end   = "06:30"
  }
}
`
	// Only some nested fields: the others keep their defaults.
	partial := `
resource "spaceman_telegram_settings" "this" {
  send_limits           = { queue_total = 100, queue_per_chat = 10 }
  notification_defaults = { digest_time = "08:15" }
}
`
	// Attributes taken out of the configuration go back to SpaceMan's defaults.
	removed := `
resource "spaceman_telegram_settings" "this" {
  send_limits = { queue_per_chat = 10 }
}
`
	accCase(t, gone,
		resource.TestStep{
			Config: first,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "id", "telegram-settings"),
				resource.TestCheckResourceAttr(addr, "link_default_max_role", "viewer"),
				resource.TestCheckResourceAttr(addr, "link_reconfirm_days", "30"),
				// Left out: SpaceMan's defaults.
				resource.TestCheckResourceAttr(addr, "links_per_user", "1"),
				resource.TestCheckResourceAttr(addr, "link_allow_admin_cap", "false"),
				resource.TestCheckResourceAttr(addr, "miniapp_session_absolute_seconds", "3600"),
				resource.TestCheckResourceAttr(addr, "send_limits.global_per_second", "25"),
				resource.TestCheckResourceAttr(addr, "send_limits.queue_total", "5000"),
				resource.TestCheckResourceAttr(addr, "inbound_limits.actions_per_minute", "30"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.digest_time", "09:00"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.inbox_items", "digest"),
				apiCheck(addr, func(ctx context.Context, c *client.Client, _ string, _ *terraform.State) error {
					s, err := c.GetTelegramSettings(ctx)
					if err != nil {
						return err
					}
					if !s.Stored || s.LinkDefaultMaxRole != "viewer" || s.LinkReconfirmDays != 30 {
						return fmt.Errorf("settings = %+v", s)
					}
					return nil
				}),
			),
		},
		resource.TestStep{Config: first, PlanOnly: true},
		resource.TestStep{
			Config: changed,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "link_default_max_role", "operator"),
				resource.TestCheckResourceAttr(addr, "link_allow_admin_cap", "true"),
				resource.TestCheckResourceAttr(addr, "link_sso_grants", "true"),
				resource.TestCheckResourceAttr(addr, "links_per_user", "2"),
				resource.TestCheckResourceAttr(addr, "topics_mode_default", "true"),
				resource.TestCheckResourceAttr(addr, "send_limits.global_per_second", "12.5"),
				resource.TestCheckResourceAttr(addr, "send_limits.chat_per_second", "0.5"),
				resource.TestCheckResourceAttr(addr, "send_limits.queue_per_chat", "40"),
				resource.TestCheckResourceAttr(addr, "inbound_limits.downloads_per_hour", "25"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.min_priority", "high"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.tasks_in_review", "off"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.quiet_hours_end", "06:30"),
				apiCheck(addr, func(ctx context.Context, c *client.Client, _ string, _ *terraform.State) error {
					s, err := c.GetTelegramSettings(ctx)
					if err != nil {
						return err
					}
					if s.SendLimits.GlobalPerSecond != 12.5 || s.InboundLimits.UpdatesPerSecond != 10 || s.NotificationDefaults.DigestTime != "07:30" ||
						s.NotificationDefaults.Digest || s.MiniAppSessionAbsoluteSeconds != 7200 {
						return fmt.Errorf("settings = %+v", s)
					}
					return nil
				}),
			),
		},
		resource.TestStep{Config: changed, PlanOnly: true},
		// Attributes that were set and are now left out go back to their defaults; the rest is as configured.
		resource.TestStep{
			Config: partial,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "send_limits.queue_total", "100"),
				resource.TestCheckResourceAttr(addr, "send_limits.queue_per_chat", "10"),
				resource.TestCheckResourceAttr(addr, "send_limits.global_per_second", "25"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.digest_time", "08:15"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.quiet_hours_end", "08:00"),
				resource.TestCheckResourceAttr(addr, "link_reconfirm_days", "90"),
				resource.TestCheckResourceAttr(addr, "link_allow_admin_cap", "false"),
			),
		},
		resource.TestStep{Config: partial, PlanOnly: true},
		// Removing a set attribute reverts it, and only the configured fields are stored.
		resource.TestStep{
			Config: removed,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "send_limits.queue_total", "5000"),
				resource.TestCheckResourceAttr(addr, "send_limits.queue_per_chat", "10"),
				resource.TestCheckResourceAttr(addr, "notification_defaults.digest_time", "09:00"),
				apiCheck(addr, func(ctx context.Context, c *client.Client, _ string, _ *terraform.State) error {
					s, err := c.GetTelegramSettings(ctx)
					if err != nil {
						return err
					}
					if !s.Stored || s.SendLimits.QueueTotal != 5000 || s.SendLimits.QueuePerChat != 10 || s.NotificationDefaults.DigestTime != "09:00" {
						return fmt.Errorf("settings = %+v", s)
					}
					return nil
				}),
			),
		},
		resource.TestStep{Config: removed, PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateId: "telegram-settings", ImportStateVerify: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateId: "other", ExpectError: regexp.MustCompile("singleton")},
	)
}

// TestAccTelegramSettingsImportThenDrop: after an import the provider has no record of which values were configured,
// so the first plan is a one-off update that seeds it; dropping an attribute afterwards then reverts it to the default.
func TestAccTelegramSettingsImportThenDrop(t *testing.T) {
	const addr = "spaceman_telegram_settings.this"
	withDays := `
resource "spaceman_telegram_settings" "this" {
  link_default_max_role = "viewer"
  link_reconfirm_days   = 30
}
`
	withoutDays := `
resource "spaceman_telegram_settings" "this" {
  link_default_max_role = "viewer"
}
`
	forget := `
removed {
  from = spaceman_telegram_settings.this
  lifecycle {
    destroy = false
  }
}
`
	accCase(t, func(*terraform.State) error { return nil },
		resource.TestStep{Config: withDays, Check: resource.TestCheckResourceAttr(addr, "link_reconfirm_days", "30")},
		// Out of the state without destroying, then imported again: the private record is gone.
		resource.TestStep{Config: forget},
		resource.TestStep{Config: withDays, ResourceName: addr, ImportState: true, ImportStateId: "telegram-settings", ImportStatePersist: true},
		// The first plan seeds the record (an update), the next one is empty.
		resource.TestStep{
			Config: withDays,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}},
			Check: resource.TestCheckResourceAttr(addr, "link_reconfirm_days", "30"),
		},
		resource.TestStep{Config: withDays, PlanOnly: true},
		resource.TestStep{
			Config: withoutDays,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}},
			Check: resource.TestCheckResourceAttr(addr, "link_reconfirm_days", "90"),
		},
		resource.TestStep{Config: withoutDays, PlanOnly: true},
	)
}

// TestAccTelegramSettingsImportThenCrossField: after an import the stored values of the leaves left out of the
// configuration are not what the next apply keeps (it stores only the configuration), so the cross-field check must use
// the defaults for them. Stored idle=2000 with a configured absolute=1000 would fail; the apply resets idle to 900.
func TestAccTelegramSettingsImportThenCrossField(t *testing.T) {
	const addr = "spaceman_telegram_settings.this"
	both := `
resource "spaceman_telegram_settings" "this" {
  miniapp_session_idle_seconds     = 2000
  miniapp_session_absolute_seconds = 3000
}
`
	onlyAbsolute := `
resource "spaceman_telegram_settings" "this" {
  miniapp_session_absolute_seconds = 1000
}
`
	forget := `
removed {
  from = spaceman_telegram_settings.this
  lifecycle {
    destroy = false
  }
}
`
	accCase(t, func(*terraform.State) error { return nil },
		resource.TestStep{Config: both, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "miniapp_session_idle_seconds", "2000"),
			resource.TestCheckResourceAttr(addr, "miniapp_session_absolute_seconds", "3000"))},
		resource.TestStep{Config: forget},
		resource.TestStep{Config: onlyAbsolute, ResourceName: addr, ImportState: true, ImportStateId: "telegram-settings", ImportStatePersist: true},
		resource.TestStep{
			Config: onlyAbsolute,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}},
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "miniapp_session_idle_seconds", "900"),
				resource.TestCheckResourceAttr(addr, "miniapp_session_absolute_seconds", "1000")),
		},
		resource.TestStep{Config: onlyAbsolute, PlanOnly: true},
	)
}

// TestAccTelegramSettingsPlanTime: out-of-range values and broken cross-field rules never reach SpaceMan (settings and
// a space's settings).
func TestAccTelegramSettingsPlanTime(t *testing.T) {
	bad := func(body string) resource.TestStep {
		return resource.TestStep{Config: "resource \"spaceman_telegram_settings\" \"this\" {\n" + body + "\n}", PlanOnly: true,
			ExpectError: regexp.MustCompile(`(?s)Error:`)}
	}
	accSteps(t,
		bad(`link_reconfirm_days = 6`),
		bad(`links_per_user = 4`),
		bad(`link_default_max_role = "admin"`),
		bad(`miniapp_session_idle_seconds = 3000
  miniapp_session_absolute_seconds = 1000`),
		bad(`send_limits = { global_per_second = 31 }`),
		bad(`send_limits = { chat_per_second = 0 }`),
		bad(`send_limits = { queue_total = 10, queue_per_chat = 20 }`),
		// One side only: the other side is SpaceMan's default (50 per chat; 23:00 to 08:00; 900 s idle).
		bad(`send_limits = { queue_total = 10 }`),
		bad(`notification_defaults = { quiet_hours_start = "08:00" }`),
		bad(`notification_defaults = { quiet_hours_end = "23:00" }`),
		bad(`miniapp_session_absolute_seconds = 900
  miniapp_session_idle_seconds = 3600`),
		bad(`inbound_limits = { updates_per_second = 101 }`),
		bad(`notification_defaults = { digest_time = "9:00" }`),
		bad(`notification_defaults = { quiet_hours_start = "22:00", quiet_hours_end = "22:00" }`),
		bad(`notification_defaults = { reminders = "daily" }`),
		resource.TestStep{Config: `resource "spaceman_space_telegram" "s" {
  space_id      = "spc_x"
  enabled       = true
  content_level = "everything"
}`, PlanOnly: true, ExpectError: regexp.MustCompile(`content_level`)},
		resource.TestStep{Config: `resource "spaceman_space_telegram" "s" {
  space_id           = "spc_x"
  enabled            = true
  enforced_approvals = "always"
}`, PlanOnly: true, ExpectError: regexp.MustCompile(`enforced_approvals`)},
	)
}

// TestAccSpaceTelegram: a space's Telegram settings are created, changed in place, imported and removed on destroy
// (Telegram is off for the space again).
func TestAccSpaceTelegram(t *testing.T) {
	testAccPreCheck(t) // before the configuration reads the acceptance environment
	const addr = "spaceman_space_telegram.s"
	slug := "tf-tg-" + suffix()
	hcl := func(body string) string {
		return fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %q
  name             = "Telegram"
  node_id          = %q
  purge_on_destroy = true
}

resource "spaceman_space_telegram" "s" {
  space_id = spaceman_space.s.id
%s
}
`, slug, acc.nodeA.ID, body)
	}
	var spaceID string
	gone := func(*terraform.State) error {
		s, err := acc.client.GetSpaceTelegram(context.Background(), spaceID)
		if client.IsNotFound(err) {
			return nil // the purged space is gone too
		}
		if err != nil {
			return err
		}
		if s.Stored || s.Enabled {
			return fmt.Errorf("the space still has Telegram settings after destroy: %+v", s)
		}
		return nil
	}
	accCase(t, gone,
		resource.TestStep{
			Config: hcl(`  enabled = true`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "enabled", "true"),
				resource.TestCheckResourceAttr(addr, "content_level", "full"),
				resource.TestCheckResourceAttr(addr, "enforced_approvals", "web_only"),
				resource.TestCheckResourceAttr(addr, "topics", "false"),
				resource.TestCheckResourceAttr(addr, "terminal_watch", "false"),
				func(s *terraform.State) error { spaceID = stateAttr(s, addr, "space_id"); return nil },
				apiCheck(addr, func(ctx context.Context, c *client.Client, id string, _ *terraform.State) error {
					s, err := c.GetSpaceTelegram(ctx, id)
					if err != nil {
						return err
					}
					if !s.Stored || !s.Enabled || s.ContentLevel != "full" {
						return fmt.Errorf("space telegram = %+v", s)
					}
					return nil
				}),
			),
		},
		resource.TestStep{Config: hcl(`  enabled = true`), PlanOnly: true},
		resource.TestStep{
			Config: hcl(`  enabled            = true
  content_level      = "summary"
  enforced_approvals = "confirm"
  topics             = true
  terminal_watch     = true`),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "content_level", "summary"),
				resource.TestCheckResourceAttr(addr, "enforced_approvals", "confirm"),
				resource.TestCheckResourceAttr(addr, "topics", "true"),
				resource.TestCheckResourceAttr(addr, "terminal_watch", "true"),
			),
		},
		// An attribute left out goes back to its default: the resource describes the whole setting.
		resource.TestStep{
			Config: hcl(`  enabled = false`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "enabled", "false"),
				resource.TestCheckResourceAttr(addr, "content_level", "full"),
				resource.TestCheckResourceAttr(addr, "topics", "false"),
			),
		},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true},
	)
}

// TestAccSpaceTelegramGroup: group_chat_id (group mode) against the fake Telegram. Left out it is unmanaged (the
// binding stays), 0 unbinds, import reads it back, and a chat the bot is not in is refused (422).
func TestAccSpaceTelegramGroup(t *testing.T) {
	testAccPreCheck(t)
	const addr = "spaceman_space_telegram.s"
	slug := "tf-tgg-" + suffix()
	group := fmt.Sprintf("%d", fakeForumChatID)
	hcl := func(body string) string {
		return fmt.Sprintf(`
resource "spaceman_telegram_bot" "bot" {
  bot_token_wo         = "3003:AAtfgroupsecret"
  bot_token_wo_version = 1
  enabled              = true
  mode                 = "polling"
}

resource "spaceman_space" "s" {
  slug             = %q
  name             = "Telegram group"
  node_id          = %q
  purge_on_destroy = true
}

resource "spaceman_space_telegram" "s" {
  depends_on = [spaceman_telegram_bot.bot]
  space_id   = spaceman_space.s.id
  enabled    = true
%s
}
`, slug, acc.nodeA.ID, body)
	}
	bound := func(want int64) resource.TestCheckFunc {
		return apiCheck(addr, func(ctx context.Context, c *client.Client, id string, _ *terraform.State) error {
			s, err := c.GetSpaceTelegram(ctx, id)
			if err != nil {
				return err
			}
			if s.GroupChatID != want {
				return fmt.Errorf("group_chat_id = %d, want %d", s.GroupChatID, want)
			}
			return nil
		})
	}
	accCase(t, nil,
		// Not managed: nothing is bound and the attribute reads 0.
		resource.TestStep{Config: hcl(``), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "group_chat_id", "0"), bound(0))},
		resource.TestStep{Config: hcl(``), PlanOnly: true},
		resource.TestStep{Config: hcl(`  group_chat_id = ` + group), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "group_chat_id", group), bound(fakeForumChatID))},
		// Left out again: the binding stays and the plan is empty.
		resource.TestStep{Config: hcl(``), PlanOnly: true},
		resource.TestStep{Config: hcl(`  content_level = "summary"`), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "group_chat_id", group), bound(fakeForumChatID))},
		resource.TestStep{Config: hcl(`  group_chat_id = 0`), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(addr, "group_chat_id", "0"), bound(0))},
		resource.TestStep{Config: hcl(`  group_chat_id = ` + group), Check: bound(fakeForumChatID)},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		// A chat the bot is not a member of is refused, and the binding stays.
		resource.TestStep{Config: hcl(`  group_chat_id = 5`), PlanOnly: true, ExpectError: regexp.MustCompile(`at most 0`)},
		resource.TestStep{Config: hcl(`  group_chat_id = -1009999999999`), ExpectError: regexp.MustCompile(`not a member`)},
	)
}
