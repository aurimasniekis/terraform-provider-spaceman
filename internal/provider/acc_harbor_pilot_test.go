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
)

// hpConfig is the configuration of TestAccHarborPilot: a space, the connection and the space's settings.
type hpConfig struct {
	parent     string
	parentVer  int
	service    string // empty: no service token
	serviceVer int    // 0: no service_token_wo_version
	space      string // the space's settings block body
	spaceSlug  string
	nodeID     string
	gatewayURL string
}

func (c hpConfig) hcl() string {
	var b strings.Builder
	fmt.Fprintf(&b, `
resource "spaceman_space" "s" {
  slug             = %q
  name             = "Harbor Pilot"
  node_id          = %q
  purge_on_destroy = true
}

resource "spaceman_harbor_pilot_connection" "hp" {
  base_url                = %q
  allow_insecure_http     = true
  parent_token_wo         = %q
  parent_token_wo_version = %d
`, c.spaceSlug, c.nodeID, c.gatewayURL, c.parent, c.parentVer)
	if c.service != "" {
		fmt.Fprintf(&b, "  service_token_wo         = %q\n", c.service)
	}
	if c.serviceVer != 0 {
		fmt.Fprintf(&b, "  service_token_wo_version = %d\n", c.serviceVer)
	}
	fmt.Fprintf(&b, `}

resource "spaceman_space_harbor_pilot" "s" {
  space_id   = spaceman_space.s.id
%s
  depends_on = [spaceman_harbor_pilot_connection.hp]
}
`, c.space)
	return b.String()
}

// noTokens fails when any Harbor Pilot token appears anywhere in the state.
func noTokens(tokens ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		for _, tok := range tokens {
			if tok != "" && strings.Contains(string(b), tok) {
				return fmt.Errorf("a Harbor Pilot token (%s…) is in the state", tok[:4])
			}
		}
		for _, p := range []string{"hpk_", "hpa_"} {
			if strings.Contains(string(b), p) {
				return fmt.Errorf("a %s… token is in the state", p)
			}
		}
		return nil
	}
}

// noTokensInPlan fails when any Harbor Pilot token appears in the plan's values: the planned values, the resource
// changes and the prior state. (The tests write tokens as literals, so the plan's copy of the configuration holds them;
// real configurations pass ephemeral values, which the configuration section never records.)
type noTokensInPlan struct{ tokens []string }

func (c noTokensInPlan) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	b, err := json.Marshal([]any{req.Plan.PlannedValues, req.Plan.ResourceChanges, req.Plan.PriorState, req.Plan.OutputChanges, req.Plan.Variables})
	if err != nil {
		resp.Error = err
		return
	}
	for _, tok := range c.tokens {
		if tok != "" && strings.Contains(string(b), tok) {
			resp.Error = fmt.Errorf("a Harbor Pilot token (%s…) is in the plan", tok[:4])
			return
		}
	}
	for _, p := range []string{"hpk_", "hpa_"} {
		if strings.Contains(string(b), p) {
			resp.Error = fmt.Errorf("a %s… token is in the plan", p)
			return
		}
	}
}

// TestAccHarborPilot (SpaceMan ADR-0060; needs SpaceMan with the Harbor Pilot gateway and a real `harbor-pilot serve
// --demo`, SPACEMAN_HARBOR_PILOT=1): the connection with write-only tokens, rotation by version only, a space's
// settings, import of both, and removing the service token.
func TestAccHarborPilot(t *testing.T) {
	hpPreCheck(t)
	g := startHarborPilot(t)
	p1, p2, sp := g.ParentTokens[0], g.ParentTokens[1], g.ServiceToken
	base := hpConfig{parent: p1, parentVer: 1, service: sp, serviceVer: 1, spaceSlug: "tf-hp-" + suffix(), nodeID: acc.nodeA.ID, gatewayURL: g.URL,
		space: fmt.Sprintf("  enabled     = true\n  project_id  = %q\n  ttl_seconds = 3600\n", g.ProjectID)}
	noLeak := noTokens(p1, p2, sp)
	planNoLeak := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{noTokensInPlan{[]string{p1, p2, sp}}}}

	rotated := base
	rotated.parent = p2 // a new token value with the same version: Terraform can't see it, so the plan is empty
	bumped := rotated
	bumped.parentVer = 2
	changed := bumped
	changed.space = fmt.Sprintf(`  enabled                      = true
  project_id                   = %q
  harnesses                    = ["claude-code"]
  ttl_seconds                  = 7200
  on_budget_status_unavailable = "refuse"
  allowed_model_aliases        = ["claude-sonnet-4-5"]
`, g.AltProjectID)
	localhost := strings.Replace(g.URL, "127.0.0.1", "localhost", 1)
	movedService := changed // a move that sends the configured service token again keeps it
	movedService.gatewayURL = localhost
	movedBack := movedService // a move without the service token, while SpaceMan holds one: refused
	movedBack.gatewayURL = g.URL
	movedBack.service = ""
	noService := changed
	noService.service, noService.serviceVer = "", 0
	moved := noService
	moved.gatewayURL = localhost

	accSteps(t,
		resource.TestStep{
			Config:           base.hcl(),
			ConfigPlanChecks: planNoLeak,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "id", "harbor-pilot"),
				resource.TestCheckResourceAttrSet("spaceman_harbor_pilot_connection.hp", "connection_id"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "binding_id", g.BindingID),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_parent_token", "true"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_service_token", "true"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "parent_token_version", "1"),
				resource.TestCheckNoResourceAttr("spaceman_harbor_pilot_connection.hp", "parent_token_wo"),
				resource.TestCheckNoResourceAttr("spaceman_harbor_pilot_connection.hp", "service_token_wo"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "enabled", "true"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "project_id", g.ProjectID),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "ttl_seconds", "3600"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "harnesses.#", "2"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "on_budget_status_unavailable", "proceed"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "allowed_model_aliases.#", "0"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "connected", "true"),
				noLeak,
			),
		},
		// A different token with the same version: nothing to do (Terraform never sees write-only values).
		resource.TestStep{Config: rotated.hcl(), PlanOnly: true},
		// Bumping the version sends the new token (SpaceMan validates it against Harbor Pilot).
		resource.TestStep{
			Config: bumped.hcl(),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_harbor_pilot_connection.hp", plancheck.ResourceActionUpdate),
				plancheck.ExpectResourceAction("spaceman_space_harbor_pilot.s", plancheck.ResourceActionNoop),
				noTokensInPlan{[]string{p1, p2, sp}}}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "parent_token_version", "2"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "service_token_version", "1"),
				noLeak,
			),
		},
		resource.TestStep{Config: bumped.hcl(), PlanOnly: true},
		// Every space setting changes in place.
		resource.TestStep{
			Config: changed.hcl(),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_space_harbor_pilot.s", plancheck.ResourceActionUpdate),
				plancheck.ExpectResourceAction("spaceman_harbor_pilot_connection.hp", plancheck.ResourceActionNoop)}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "project_id", g.AltProjectID),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "harnesses.#", "1"),
				resource.TestCheckTypeSetElemAttr("spaceman_space_harbor_pilot.s", "harnesses.*", "claude-code"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "ttl_seconds", "7200"),
				resource.TestCheckResourceAttr("spaceman_space_harbor_pilot.s", "on_budget_status_unavailable", "refuse"),
				resource.TestCheckTypeSetElemAttr("spaceman_space_harbor_pilot.s", "allowed_model_aliases.*", "claude-sonnet-4-5"),
			),
		},
		resource.TestStep{Config: changed.hcl(), PlanOnly: true},
		// Import: the connection by its singleton ID, the settings by the space ID. Write-only values are never read.
		resource.TestStep{ResourceName: "spaceman_harbor_pilot_connection.hp", ImportState: true, ImportStateId: "harbor-pilot", ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"parent_token_wo", "parent_token_wo_version", "service_token_wo", "service_token_wo_version"}},
		resource.TestStep{ResourceName: "spaceman_space_harbor_pilot.s", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "spaceman_harbor_pilot_connection.hp", ImportState: true, ImportStateId: "hpc_other",
			ExpectError: regexp.MustCompile(`singleton`)},
		// A new gateway address (the same gateway as localhost) sends both configured tokens again with the same versions:
		// SpaceMan sends stored tokens only to the address they were entered for, and the service token is kept.
		resource.TestStep{
			Config: movedService.hcl(),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_harbor_pilot_connection.hp", plancheck.ResourceActionUpdate),
				noTokensInPlan{[]string{p1, p2, sp}}}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "base_url", localhost),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_parent_token", "true"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "parent_token_version", "3"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_service_token", "true"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "service_token_version", "2"),
				noLeak,
			),
		},
		resource.TestStep{Config: movedService.hcl(), PlanOnly: true},
		// Moving with the service token left out but its version unchanged doesn't remove it: SpaceMan refuses the move,
		// the error says how to go on, and nothing changes (the next plan of the previous configuration is empty).
		resource.TestStep{
			Config:      movedBack.hcl(),
			ExpectError: regexp.MustCompile(`(?s)add service_token_wo, or bump\s+service_token_wo_version to remove it`),
		},
		resource.TestStep{Config: movedService.hcl(), PlanOnly: true},
		resource.TestStep{
			Config: movedService.hcl(),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "base_url", localhost),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "parent_token_version", "3"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_service_token", "true"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "service_token_version", "2"),
			),
		},
		// Leaving out the service token and its version removes it from SpaceMan (the address moves back too).
		resource.TestStep{
			Config:           noService.hcl(),
			ConfigPlanChecks: planNoLeak,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_service_token", "false"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "service_token_version", "3"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_parent_token", "true"),
				noLeak,
			),
		},
		resource.TestStep{Config: noService.hcl(), PlanOnly: true},
		// A new gateway address (the same gateway as localhost) sends the parent token again with the same version:
		// SpaceMan sends stored tokens only to the address they were entered for.
		resource.TestStep{
			Config: moved.hcl(),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_harbor_pilot_connection.hp", plancheck.ResourceActionUpdate),
				noTokensInPlan{[]string{p1, p2, sp}}}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "base_url", moved.gatewayURL),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "parent_token_version", "5"),
				resource.TestCheckResourceAttr("spaceman_harbor_pilot_connection.hp", "has_service_token", "false"),
				noLeak,
			),
		},
		resource.TestStep{Config: moved.hcl(), PlanOnly: true},
	)
	// The test case's destroy removed the space's settings, then the connection.
	if _, err := acc.client.GetHarborPilotConnection(context.Background()); err == nil {
		t.Fatal("the Harbor Pilot connection is still there after destroy")
	}
}
