package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// tgNoToken fails when a bot token (or its secret half) appears anywhere in the state: the decoded state and, from
// the raw terraform.tfstate under stateDir, the file itself and the base64 "private" blobs (and their values).
func tgNoToken(stateDir string, tokens ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		hay := []string{string(b)}
		raw, err := tgRawState(stateDir)
		if err != nil {
			return err
		}
		hay = append(hay, raw...)
		for _, tok := range tokens {
			_, secret, _ := strings.Cut(tok, ":")
			for _, h := range hay {
				if strings.Contains(h, tok) || strings.Contains(h, secret) {
					return fmt.Errorf("a bot token (%s…) is in the state", tok[:4])
				}
			}
		}
		return nil
	}
}

// tgRawState returns the text of terraform.tfstate under dir plus each decoded "private" value in it. It fails if
// there is no state file, so the check cannot pass by looking at nothing.
func tgRawState(dir string) ([]string, error) {
	var file string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "terraform.tfstate" {
			file = p
		}
		return nil
	})
	if file == "" {
		return nil, fmt.Errorf("no terraform.tfstate under %s", dir)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	out := []string{string(b)}
	var st struct {
		Resources []struct {
			Instances []struct {
				Private string `json:"private"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	n := 0
	for _, r := range st.Resources {
		for _, i := range r.Instances {
			if i.Private == "" {
				continue
			}
			n++
			dec, err := base64.StdEncoding.DecodeString(i.Private)
			if err != nil {
				return nil, fmt.Errorf("private state is not base64: %w", err)
			}
			out = append(out, string(dec))
			// The framework keeps private state as a JSON map of base64 values.
			var kv map[string]string
			if json.Unmarshal(dec, &kv) == nil {
				for _, v := range kv {
					if d2, err := base64.StdEncoding.DecodeString(v); err == nil {
						out = append(out, string(d2))
					}
				}
			}
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("no private state in %s: the leak check would not see it", file)
	}
	return out, nil
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
	stateDir := t.TempDir()
	t.Setenv("TF_ACC_TEMP_DIR", stateDir) // the working directory, so the check can read terraform.tfstate
	noLeak := tgNoToken(stateDir, t1, t2)
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

// enabled = true in webhook mode needs central's public URL to be https, which the dev server's is not: central's
// refusal comes back as the apply error and nothing is created. The success path (a https public URL registering the
// webhook) is covered by go-spaceman's tgbot tests.
func TestAccTelegramBotWebhookNeedsHTTPS(t *testing.T) {
	accSteps(t, resource.TestStep{
		Config:      tgBotConfig{token: "4004:AAtfsecretfour", version: 1, body: `  enabled = true`}.hcl(),
		ExpectError: regexp.MustCompile(`webhook mode needs central's public_url to be https`),
	})
}
