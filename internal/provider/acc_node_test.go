package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func nodeHCL(name, desc string, enabled bool, enrollmentVersion int) string {
	return fmt.Sprintf(`
resource "spaceman_node" "n" {
  name               = %q
  description        = %q
  labels             = { zone = "eu" }
  enabled            = %t
  enrollment_version = %d
}
`, name, desc, enabled, enrollmentVersion)
}

// A node is registered with a one-time enrollment token, edited in place (name, description, labels), disabled and
// enabled, issues a new token when enrollment_version changes, imports without its token, and is revoked on destroy.
func TestAccNode(t *testing.T) {
	testAccPreCheck(t)
	name := "tf-node-" + suffix()
	var nodeID, firstToken string
	accCase(t,
		func(s *terraform.State) error {
			if nodeID == "" {
				return nil
			}
			n, err := acc.client.GetNode(context.Background(), nodeID)
			if err != nil {
				return err
			}
			if n.State != "revoked" {
				return fmt.Errorf("node %s is %s after destroy, want revoked", nodeID, n.State)
			}
			return nil
		},
		resource.TestStep{
			Config: nodeHCL(name, "first", true, 1),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_node.n", "state", "pending"),
				resource.TestCheckResourceAttr("spaceman_node.n", "labels.zone", "eu"),
				resource.TestCheckResourceAttrSet("spaceman_node.n", "enrollment_expires_at"),
				func(s *terraform.State) error {
					nodeID, firstToken = stateAttr(s, "spaceman_node.n", "id"), stateAttr(s, "spaceman_node.n", "enrollment_token")
					if !strings.HasPrefix(firstToken, "spaceman_en_") {
						return fmt.Errorf("enrollment token %q", firstToken)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config: nodeHCL(name+"-x", "second", false, 1),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_node.n", plancheck.ResourceActionUpdate)}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_node.n", "name", name+"-x"),
				resource.TestCheckResourceAttr("spaceman_node.n", "description", "second"),
				resource.TestCheckResourceAttr("spaceman_node.n", "state", "disabled"),
				func(s *terraform.State) error {
					if got := stateAttr(s, "spaceman_node.n", "enrollment_token"); got != firstToken {
						return fmt.Errorf("an edit changed the enrollment token")
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config: nodeHCL(name+"-x", "second", true, 2),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_node.n", "state", "active"),
				func(s *terraform.State) error {
					if got := stateAttr(s, "spaceman_node.n", "enrollment_token"); got == "" || got == firstToken {
						return fmt.Errorf("enrollment_version did not issue a new token")
					}
					return nil
				},
			),
		},
		resource.TestStep{
			ResourceName: "spaceman_node.n", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"enrollment_token", "enrollment_expires_at", "enrollment_version", "revoke_on_destroy"},
		},
		// Edited outside Terraform: drift.
		resource.TestStep{
			PreConfig: func() {
				ctx := context.Background()
				n, err := acc.client.GetNode(ctx, nodeID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := acc.client.UpdateNode(ctx, nodeID, n.Version, client.CreateNodeInput{Name: n.Name, Description: "edited in the UI", Labels: n.Labels}); err != nil {
					t.Fatal(err)
				}
			},
			Config:             nodeHCL(name+"-x", "second", true, 2),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
	)
}
