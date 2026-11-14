package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

type tgBotConfig struct {
	token   string
	version int
	body    string // the rest of the resource body
}

func (c tgBotConfig) hcl() string {
	return fmt.Sprintf(`
resource "spaceman_telegram_bot" "bot" {
  bot_token_wo         = %q
  bot_token_wo_version = %d
%s
}
`, c.token, c.version, c.body)
}

// tgNoToken fails when a bot token (or its secret half) appears anywhere in the state.
func tgNoToken(tokens ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		for _, tok := range tokens {
			_, secret, _ := strings.Cut(tok, ":")
			if strings.Contains(string(b), tok) || strings.Contains(string(b), secret) {
				return fmt.Errorf("a bot token (%s…) is in the state", tok[:4])
			}
		}
		return nil
	}
}

type tgNoTokenInPlan struct{ tokens []string }

func (c tgNoTokenInPlan) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	b, err := json.Marshal([]any{req.Plan.PlannedValues, req.Plan.ResourceChanges, req.Plan.PriorState, req.Plan.OutputChanges})
	if err != nil {
		resp.Error = err
		return
	}
	for _, tok := range c.tokens {
		_, secret, _ := strings.Cut(tok, ":")
		if strings.Contains(string(b), tok) || strings.Contains(string(b), secret) {
			resp.Error = fmt.Errorf("a bot token (%s…) is in the plan", tok[:4])
			return
		}
	}
}

// TestAccTelegramBot (SpaceMan ADR-0083; needs SpaceMan with the Telegram bot operations): the singleton with a
// write-only token against a fake Bot API, rotation by version only, every setting in place, a different bot, import
// and destroy. The token is never in the state or the plan.
func TestAccTelegramBot(t *testing.T) {
	t1, t2 := "1001:AAtfsecretone", "2002:AAtfsecrettwo"
	noLeak := tgNoToken(t1, t2)
	planNoLeak := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{tgNoTokenInPlan{[]string{t1, t2}}}}
	base := tgBotConfig{token: t1, version: 1}
	settings := tgBotConfig{token: t1, version: 1, body: `
  enabled              = true
  mode                 = "polling"
  miniapp_host         = "tg.central.example.com"
  webhook_ip_allowlist = ["149.154.160.0/20", "91.108.4.0/22"]
`}
	rotated := settings
	rotated.token = t2 // a new value with the same version: Terraform can't see it, so the plan is empty
	other := rotated
	other.version = 2 // a different bot: another bot ID

	const addr = "spaceman_telegram_bot.bot"
	gone := func(*terraform.State) error {
		if _, err := acc.client.GetTelegramBot(context.Background()); !client.IsNotFound(err) {
			return fmt.Errorf("the bot still exists after destroy (err=%w)", err)
		}
		return nil
	}
	accCase(t, gone,
		resource.TestStep{
			Config:           base.hcl(),
			ConfigPlanChecks: planNoLeak,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "id", "telegram-bot"),
				resource.TestCheckResourceAttr(addr, "enabled", "false"),
				resource.TestCheckResourceAttr(addr, "mode", "webhook"),
				resource.TestCheckResourceAttr(addr, "miniapp_host", ""),
				resource.TestCheckResourceAttr(addr, "webhook_ip_allowlist.#", "0"),
				resource.TestCheckResourceAttr(addr, "bot_id", "1001"),
				resource.TestCheckResourceAttr(addr, "username", "tf_bot_1001"),
				resource.TestCheckResourceAttr(addr, "has_token", "true"),
				resource.TestCheckResourceAttr(addr, "token_version", "1"),
				resource.TestCheckResourceAttrSet(addr, "webhook_status"),
				resource.TestCheckNoResourceAttr(addr, "bot_token_wo"),
				noLeak,
			),
		},
		resource.TestStep{Config: base.hcl(), PlanOnly: true},
		// Every setting changes in place, without sending the token again.
		resource.TestStep{
			Config: settings.hcl(),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate), tgNoTokenInPlan{[]string{t1, t2}}}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "enabled", "true"),
				resource.TestCheckResourceAttr(addr, "mode", "polling"),
				resource.TestCheckResourceAttr(addr, "miniapp_host", "tg.central.example.com"),
				resource.TestCheckResourceAttr(addr, "webhook_ip_allowlist.#", "2"),
				resource.TestCheckTypeSetElemAttr(addr, "webhook_ip_allowlist.*", "149.154.160.0/20"),
				resource.TestCheckResourceAttr(addr, "bot_id", "1001"),
				resource.TestCheckResourceAttr(addr, "token_version", "1"),
				noLeak,
			),
		},
		resource.TestStep{Config: settings.hcl(), PlanOnly: true},
		resource.TestStep{Config: rotated.hcl(), PlanOnly: true},
		// Bumping the version sends the new token: another bot.
		resource.TestStep{
			Config: other.hcl(),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate), tgNoTokenInPlan{[]string{t1, t2}}}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "bot_id", "2002"),
				resource.TestCheckResourceAttr(addr, "username", "tf_bot_2002"),
				resource.TestCheckResourceAttr(addr, "token_version", "2"),
				noLeak,
			),
		},
		resource.TestStep{Config: other.hcl(), PlanOnly: true},
		resource.TestStep{ResourceName: addr, ImportState: true, ImportStateId: "telegram-bot", ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"bot_token_wo", "bot_token_wo_version", "webhook_status"}},
	)
}

// A malformed allowlist entry fails at plan time, before anything is sent.
func TestAccTelegramBotBadCIDR(t *testing.T) {
	accSteps(t, resource.TestStep{
		Config:      tgBotConfig{token: "3003:AAtfsecretthree", version: 1, body: `  webhook_ip_allowlist = ["10.0.0.1/8"]`}.hcl(),
		PlanOnly:    true,
		ExpectError: regexp.MustCompile(`canonical`),
	})
}
