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

func ingestOwnHCL(sfx, spaceSlug, nodeID, pulseURL, creds, subject string, own bool) string {
	return fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %[2]q
  name             = "Ingest own"
  node_id          = %[3]q
  purge_on_destroy = true
}

resource "spaceman_event_connection" "c" {
  name             = "tf-pulse-own-%[1]s"
  url              = %[4]q
  creds_wo         = %[5]q
  creds_wo_version = 1
}

resource "spaceman_event_subscription" "own" {
  space_id      = spaceman_space.s.id
  name          = "run dialogs"
  connection_id = spaceman_event_connection.c.id
  ingest_own    = %[7]t
  custom = jsonencode({
    subject = %[6]q
    title   = "A run is stopped at a dialog"
  })
}
`, sfx, spaceSlug, nodeID, pulseURL, creds, subject, own)
}

// TestAccEventSubscriptionIngestOwn: ingest_own is accepted on a run dialog subject, can be switched off again, and
// SpaceMan refuses it on any other subject (SpaceMan ADR-0104).
func TestAccEventSubscriptionIngestOwn(t *testing.T) {
	testAccPreCheck(t)
	sfx, slug := suffix(), "tf-own-"+suffix()
	const dialog = "evt.spaceman.fleet.run.dialog_detected"
	hcl := func(subject string, own bool) string {
		return ingestOwnHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, subject, own)
	}
	accSteps(t,
		resource.TestStep{
			Config: hcl(dialog, true),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_event_subscription.own", "ingest_own", "true"),
				apiCheck("spaceman_event_subscription.own", func(ctx context.Context, c *client.Client, id string, s *terraform.State) error {
					sub, err := c.GetSubscription(ctx, stateAttr(s, "spaceman_event_subscription.own", "space_id"), id)
					if err != nil {
						return err
					}
					if !sub.IngestOwn {
						return fmt.Errorf("subscription ingest_own = false, want true")
					}
					return nil
				}),
			),
		},
		resource.TestStep{Config: hcl(dialog, true), PlanOnly: true},
		resource.TestStep{Config: hcl(dialog, false), Check: resource.TestCheckResourceAttr("spaceman_event_subscription.own", "ingest_own", "false")},
		resource.TestStep{Config: strings.Replace(hcl("evt.spaceman.inbox.>", true), "  ingest_own", "  start         = { policy = \"new\" }\n  ingest_own", 1), ExpectError: regexp.MustCompile(`(?s)ingest_own is not allowed`)},
		resource.TestStep{ResourceName: "spaceman_event_subscription.own", ImportState: true, ImportStateVerify: true,
			ImportStateIdFunc: func(s *terraform.State) (string, error) {
				return stateAttr(s, "spaceman_event_subscription.own", "space_id") + "/" + stateAttr(s, "spaceman_event_subscription.own", "id"), nil
			},
			ImportStateVerifyIgnore: []string{"template_version_id", "custom"}},
	)
}
