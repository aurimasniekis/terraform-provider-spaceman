package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func harnessVersionHCL(sfx, harness, name, version string) string {
	hv := ""
	if version != "-" {
		hv = fmt.Sprintf("harness_version = %q", version)
	}
	return fmt.Sprintf(`
resource "spaceman_harness_profile" "v" {
  slug    = "tf-pin-%[1]s"
  name    = %[3]q
  harness = %[2]q
  %[4]s
}
`, sfx, harness, name, hv)
}

// TestAccHarnessVersion covers a profile's Claude Code version pin (SpaceMan ADR-0077): an unset attribute reads the
// server's pin without a diff and never clears it (on an unrelated update too, and when the attribute is removed), a
// configured pin is applied and imported, `""` clears it, and bad values or a codex profile fail at plan time.
func TestAccHarnessVersion(t *testing.T) {
	testAccPreCheck(t)
	sfx := suffix()
	const res = "spaceman_harness_profile.v"
	var id string
	serverPin := func(want string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			p, err := acc.client.GetProfile(context.Background(), id)
			if err != nil {
				return err
			}
			if p.HarnessVersion != want {
				return fmt.Errorf("server pin %q, want %q", p.HarnessVersion, want)
			}
			return nil
		}
	}
	accSteps(t,
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "claude-code", "Pin", "-"),
			Check: resource.ComposeAggregateTestCheckFunc(
				func(s *terraform.State) error { id = stateAttr(s, res, "id"); return nil },
				resource.TestCheckResourceAttr(res, "harness_version", ""),
			),
		},
		// A pin set outside Terraform: the unset attribute shows no change, and an unrelated update keeps it.
		resource.TestStep{
			PreConfig: func() {
				p, err := acc.client.GetProfile(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				pin := "stable"
				if _, err := acc.client.SaveProfile(context.Background(), id, p.Version, client.ProfileInput{Name: p.Name, Settings: p.Settings,
					HarnessVersion: &pin}); err != nil {
					t.Fatal(err)
				}
			},
			Config:   harnessVersionHCL(sfx, "claude-code", "Pin", "-"),
			PlanOnly: true,
		},
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "claude-code", "Pin renamed", "-"),
			Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(res, "harness_version", "stable"), serverPin("stable")),
		},
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "claude-code", "Pin renamed", "2.1.3"),
			Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(res, "harness_version", "2.1.3"), serverPin("2.1.3")),
		},
		resource.TestStep{Config: harnessVersionHCL(sfx, "claude-code", "Pin renamed", "2.1.3"), PlanOnly: true},
		resource.TestStep{ResourceName: res, ImportState: true, ImportStateVerify: true},
		// A configured pin is enforced: a change elsewhere shows as drift and is put back.
		resource.TestStep{
			PreConfig: func() {
				p, err := acc.client.GetProfile(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				pin := "latest"
				if _, err := acc.client.SaveProfile(context.Background(), id, p.Version, client.ProfileInput{Name: p.Name, Settings: p.Settings,
					HarnessVersion: &pin}); err != nil {
					t.Fatal(err)
				}
			},
			Config:             harnessVersionHCL(sfx, "claude-code", "Pin renamed", "2.1.3"),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "claude-code", "Pin renamed", "2.1.3"),
			Check:  serverPin("2.1.3"),
		},
		// Removing the attribute keeps the pin, in the state and in SpaceMan.
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "claude-code", "Pin", "-"),
			Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(res, "harness_version", "2.1.3"), serverPin("2.1.3")),
		},
		// "" clears it.
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "claude-code", "Pin", ""),
			Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(res, "harness_version", ""), serverPin("")),
		},
		resource.TestStep{Config: harnessVersionHCL(sfx, "claude-code", "Pin", ""), PlanOnly: true},
		resource.TestStep{
			Config:      harnessVersionHCL(sfx, "claude-code", "Pin", "2.1"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`exact version`),
		},
		resource.TestStep{
			Config:      harnessVersionHCL(sfx, "claude-code", "Pin", "02.1.3"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`exact version`),
		},
		// The last step must be valid: the post-test destroy uses its configuration.
		resource.TestStep{Config: harnessVersionHCL(sfx, "claude-code", "Pin", "-"), Check: serverPin("")},
	)
}

// A codex profile cannot pin a harness version: refused at plan time. `""` (no pin) is accepted.
func TestAccHarnessVersionCodex(t *testing.T) {
	testAccPreCheck(t)
	sfx := suffix()
	accSteps(t,
		resource.TestStep{
			Config:      harnessVersionHCL(sfx, "codex", "Codex pin", "latest"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`(?i)only claude-code\s+profiles can pin a harness version`),
		},
		resource.TestStep{
			Config: harnessVersionHCL(sfx, "codex", "Codex pin", ""),
			Check:  resource.TestCheckResourceAttr("spaceman_harness_profile.v", "harness_version", ""),
		},
	)
}
