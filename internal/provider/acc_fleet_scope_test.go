package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// fleetScopeHCL is the ingest_own space and connection plus an optional grant and a subscription at the given scope.
func fleetScopeHCL(sfx, spaceSlug, nodeID, pulseURL, creds, scope, reason string, grant bool) string {
	hcl := ingestOwnHCL(sfx, spaceSlug, nodeID, pulseURL, creds, "evt.spaceman.fleet.run.dialog_detected", true)
	hcl = strings.Replace(hcl, "  ingest_own    = true\n", "  ingest_own    = true\n  ingest_own_scope = "+fmt.Sprintf("%q", scope)+"\n", 1)
	if grant {
		hcl += fmt.Sprintf(`
resource "spaceman_event_fleet_scope_grant" "g" {
  space_id = spaceman_space.s.id
  reason   = %q
}
`, reason)
		hcl = strings.Replace(hcl, "  custom = jsonencode", "  depends_on = [spaceman_event_fleet_scope_grant.g]\n  custom = jsonencode", 1)
	}
	return hcl
}

// TestAccEventFleetScope: without a grant fleet scope is refused; with one it is accepted, shows no drift, the grant's
// reason is replaced in place; the subscription goes back to space before the grant is revoked (SpaceMan ADR-0106).
// The acceptance client's token must be operator-level.
func TestAccEventFleetScope(t *testing.T) {
	testAccPreCheck(t)
	sfx, slug := suffix(), "tf-fleet-"+suffix()
	hcl := func(scope, reason string, grant bool) string {
		return fleetScopeHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, scope, reason, grant)
	}
	accSteps(t,
		resource.TestStep{Config: hcl("fleet", "", false), ExpectError: regexp.MustCompile(`(?s)fleet-scope grant`)},
		resource.TestStep{
			Config: hcl("fleet", "ops space", true),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_event_subscription.own", "ingest_own_scope", "fleet"),
				resource.TestCheckResourceAttr("spaceman_event_fleet_scope_grant.g", "reason", "ops space"),
				resource.TestCheckResourceAttrSet("spaceman_event_fleet_scope_grant.g", "granted_at"),
				resource.TestCheckResourceAttrSet("spaceman_event_fleet_scope_grant.g", "granted_by"),
				func(s *terraform.State) error {
					sub, err := acc.client.GetSubscription(context.Background(), stateAttr(s, "spaceman_event_subscription.own", "space_id"), stateAttr(s, "spaceman_event_subscription.own", "id"))
					if err != nil {
						return err
					}
					if sub.IngestOwnScope != "fleet" {
						return fmt.Errorf("subscription ingest_own_scope = %q, want fleet", sub.IngestOwnScope)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: hcl("fleet", "ops space", true), PlanOnly: true},
		resource.TestStep{Config: hcl("fleet", "ops space, renamed", true), Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("spaceman_event_fleet_scope_grant.g", "reason", "ops space, renamed"),
			resource.TestCheckResourceAttr("spaceman_event_fleet_scope_grant.g", "fleet_subscriptions", "1"),
		)},
		resource.TestStep{ResourceName: "spaceman_event_fleet_scope_grant.g", ImportState: true, ImportStateVerify: true, ImportStateId: "", ImportStateIdFunc: func(s *terraform.State) (string, error) {
			return stateAttr(s, "spaceman_event_fleet_scope_grant.g", "space_id"), nil
		}},
		resource.TestStep{Config: hcl("space", "ops space, renamed", true), Check: resource.TestCheckResourceAttr("spaceman_event_subscription.own", "ingest_own_scope", "space")},
		resource.TestStep{Config: hcl("space", "", false), Check: func(s *terraform.State) error {
			_, err := acc.client.GetFleetScopeGrant(context.Background(), stateAttr(s, "spaceman_space.s", "id"))
			if !client.IsNotFound(err) {
				return fmt.Errorf("grant still present after destroy: %w", err)
			}
			return nil
		}},
	)
}
