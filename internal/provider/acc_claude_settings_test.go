package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func claudeProfileHCL(sfx, harness, claude string) string {
	return fmt.Sprintf(`
resource "spaceman_harness_profile" "c" {
  slug     = "tf-claude-%[1]s"
  name     = "TF Claude"
  harness  = %[2]q
  settings = { permission_mode = "default" }
  %[3]s
}
`, sfx, harness, claude)
}

// TestAccClaudeSettings covers a profile's Claude Code settings (SpaceMan ADR-0059): any settings.json keys through
// jsonencode or as text, no diff from the server's key order, import, removing the attribute clears them in SpaceMan, and the
// server's checks (shape of the keys it edits, claude-code only) are reported.
func TestAccClaudeSettings(t *testing.T) {
	testAccPreCheck(t)
	sfx := suffix()
	full := `claude_settings = jsonencode({
    permissions = { defaultMode = "auto", deny = ["Read(./secrets/**)"] }
    autoMode = {
      environment = ["$defaults", "Source control: GitHub repositories of example-org."]
      allow       = ["$defaults", "Pushing feature branches is allowed."]
    }
    env   = { FOO = "bar" }
    hooks = { Stop = [{ hooks = [{ type = "command", command = "true" }] }] }
  })`
	// The same settings as text with keys in another order: Terraform shows the rewrite once, and the state keeps the
	// text as written although SpaceMan returns its own key order.
	sameText := `claude_settings = <<-EOT
    {
      "hooks": {"Stop": [{"hooks": [{"command": "true", "type": "command"}]}]},
      "env": {"FOO": "bar"},
      "autoMode": {"allow": ["$defaults", "Pushing feature branches is allowed."], "environment": ["$defaults", "Source control: GitHub repositories of example-org."]},
      "permissions": {"deny": ["Read(./secrets/**)"], "defaultMode": "auto"}
    }
  EOT`
	var id string
	serverSettings := func() (map[string]any, error) {
		p, err := acc.client.GetProfile(context.Background(), id)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(p.ClaudeSettings, &m)
	}
	accSteps(t,
		resource.TestStep{
			Config: claudeProfileHCL(sfx, "claude-code", full),
			Check: func(s *terraform.State) error {
				id = stateAttr(s, "spaceman_harness_profile.c", "id")
				var m map[string]any
				if err := json.Unmarshal([]byte(stateAttr(s, "spaceman_harness_profile.c", "claude_settings")), &m); err != nil {
					return err
				}
				if m["hooks"] == nil || m["permissions"].(map[string]any)["defaultMode"] != "auto" {
					return fmt.Errorf("claude_settings in state: %v", m)
				}
				return nil
			},
		},
		resource.TestStep{Config: claudeProfileHCL(sfx, "claude-code", full), PlanOnly: true},
		resource.TestStep{ResourceName: "spaceman_harness_profile.c", ImportState: true, ImportStateVerify: true},
		resource.TestStep{Config: claudeProfileHCL(sfx, "claude-code", sameText)},
		resource.TestStep{Config: claudeProfileHCL(sfx, "claude-code", sameText), PlanOnly: true},
		resource.TestStep{
			Config: claudeProfileHCL(sfx, "claude-code", `claude_settings = jsonencode({ model = "opus" })`),
			Check: func(*terraform.State) error {
				m, err := serverSettings()
				if err != nil || len(m) != 1 || m["model"] != "opus" {
					return fmt.Errorf("server settings %v (%w)", m, err)
				}
				return nil
			},
		},
		// Removing the attribute clears the settings in SpaceMan, not only in the state.
		resource.TestStep{
			Config: claudeProfileHCL(sfx, "claude-code", ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckNoResourceAttr("spaceman_harness_profile.c", "claude_settings"),
				func(*terraform.State) error {
					if m, err := serverSettings(); err != nil || len(m) != 0 {
						return fmt.Errorf("settings not cleared: %v (%w)", m, err)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: claudeProfileHCL(sfx, "claude-code", ""), PlanOnly: true},
		resource.TestStep{
			Config:      claudeProfileHCL(sfx, "claude-code", `claude_settings = "[1]"`),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`Not a JSON object`),
		},
		resource.TestStep{
			Config:      claudeProfileHCL(sfx, "claude-code", `claude_settings = jsonencode({ permissions = { defaultMode = "yolo" } })`),
			ExpectError: regexp.MustCompile(`defaultMode`),
		},
	)
}

// A codex profile cannot have Claude Code settings.
func TestAccClaudeSettingsCodexRefused(t *testing.T) {
	testAccPreCheck(t)
	accSteps(t, resource.TestStep{
		Config:      claudeProfileHCL(suffix(), "codex", `claude_settings = jsonencode({ permissions = { deny = ["Read(./x)"] } })`),
		ExpectError: regexp.MustCompile(`only a\s+claude-code\s+profile`),
	})
}

// TestAccClaudeSettingsEmptyObject covers `claude_settings = jsonencode({})`: SpaceMan stores no settings and returns
// none, and the state keeps the configured `{}`, so the apply is consistent and a second plan is empty. SpaceMan cannot
// tell `{}` from no settings, so an import reads the attribute as null, the same as leaving it out.
func TestAccClaudeSettingsEmptyObject(t *testing.T) {
	testAccPreCheck(t)
	sfx := suffix()
	empty := claudeProfileHCL(sfx, "claude-code", `claude_settings = jsonencode({})`)
	accSteps(t,
		resource.TestStep{
			Config: empty,
			Check:  resource.TestCheckResourceAttr("spaceman_harness_profile.c", "claude_settings", "{}"),
		},
		resource.TestStep{Config: empty, PlanOnly: true},
		resource.TestStep{
			ResourceName: "spaceman_harness_profile.c", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"claude_settings"},
			ImportStateCheck: func(s []*terraform.InstanceState) error {
				if len(s) != 1 {
					return fmt.Errorf("imported %d resources", len(s))
				}
				if v, ok := s[0].Attributes["claude_settings"]; ok {
					return fmt.Errorf("imported claude_settings = %q, want null", v)
				}
				return nil
			},
		},
		// From settings to `{}` clears them in SpaceMan and keeps `{}` in the state.
		resource.TestStep{Config: claudeProfileHCL(sfx, "claude-code", `claude_settings = jsonencode({ model = "opus" })`)},
		resource.TestStep{
			Config: empty,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harness_profile.c", "claude_settings", "{}"),
				func(s *terraform.State) error {
					p, err := acc.client.GetProfile(context.Background(), stateAttr(s, "spaceman_harness_profile.c", "id"))
					if err != nil {
						return err
					}
					var m map[string]any
					if len(p.ClaudeSettings) > 0 {
						if err := json.Unmarshal(p.ClaudeSettings, &m); err != nil {
							return err
						}
					}
					if len(m) != 0 {
						return fmt.Errorf("settings not cleared: %v", m)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: empty, PlanOnly: true},
	)
}
