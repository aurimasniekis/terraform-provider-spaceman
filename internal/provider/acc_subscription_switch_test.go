package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// switchHCL has two templates with the same subject and optional variables, and three subscriptions that move between
// them: "pinned" names one variable (SpaceMan stores the other as ""), "defaults" names none and follows the latest
// version, and "moving" switches between a template version and a custom definition. use picks the template ("a" or
// "b"); custom puts "moving" on a custom definition.
func switchHCL(sfx, spaceSlug, nodeID, pulseURL, creds, use string, custom bool) string {
	moving := `template_version_id = local.version`
	if custom {
		moving = `custom              = jsonencode(local.definition)`
	}
	return fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %[2]q
  name             = "Template switch"
  node_id          = %[3]q
  purge_on_destroy = true
}

resource "spaceman_event_connection" "c" {
  name             = "tf-pulse-switch-%[1]s"
  url              = %[4]q
  creds_wo         = %[5]q
  creds_wo_version = 1
}

locals {
  definition = {
    subject = "evt.github.pull_request.>"
    variables = [
      { name = "repository", description = "owner/name; empty for every repository" },
      { name = "owner", description = "an account; empty for every account" },
    ]
    filters = [
      { path = "data.repository.full_name", op = "eq", value = "{{vars.repository}}", if_set = "repository" },
      { path = "data.repository.owner", op = "eq", value = "{{vars.owner}}", if_set = "owner" },
    ]
    title = "PR {{data.action}}"
  }
  versions = {
    a = spaceman_event_template.a.latest_version_id
    b = spaceman_event_template.b.latest_version_id
  }
  version = local.versions[%[6]q]
}

resource "spaceman_event_template" "a" {
  slug       = "tf-switch-a-%[1]s"
  name       = "TF switch A"
  definition = jsonencode(local.definition)
}

resource "spaceman_event_template" "b" {
  slug       = "tf-switch-b-%[1]s"
  name       = "TF switch B"
  definition = jsonencode(merge(local.definition, { title = "Pull request {{data.action}}" }))
}

resource "spaceman_event_subscription" "pinned" {
  space_id            = spaceman_space.s.id
  name                = "pinned"
  connection_id       = spaceman_event_connection.c.id
  template_version_id = local.version
  vars                = { repository = "acme/api" }
}

resource "spaceman_event_subscription" "defaults" {
  space_id            = spaceman_space.s.id
  name                = "defaults"
  connection_id       = spaceman_event_connection.c.id
  template_version_id = local.version
  follow              = "latest"
}

resource "spaceman_event_subscription" "moving" {
  space_id      = spaceman_space.s.id
  name          = "moving"
  connection_id = spaceman_event_connection.c.id
  %[7]s
  vars          = { repository = "acme/web" }
}
`, sfx, spaceSlug, nodeID, pulseURL, creds, use, moving)
}

// onTemplate checks that a subscription records the template and version it uses.
func onTemplate(sub, tmpl string) resource.TestCheckFunc {
	return resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttrPair("spaceman_event_subscription."+sub, "template_id", "spaceman_event_template."+tmpl, "id"),
		resource.TestCheckResourceAttrPair("spaceman_event_subscription."+sub, "template_version_id", "spaceman_event_template."+tmpl, "latest_version_id"),
	)
}

// TestAccEventSubscriptionTemplateSwitch: moving a subscription to a version of another template (or to and from a
// custom definition) applies cleanly and plans no change afterwards. template_id, and vars the configuration leaves
// out, are recomputed; the optional variable SpaceMan stores as "" is not drift.
func TestAccEventSubscriptionTemplateSwitch(t *testing.T) {
	testAccPreCheck(t)
	sfx, slug := suffix(), "tf-switch-"+suffix()
	hcl := func(use string, custom bool) string {
		return switchHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, use, custom)
	}
	storedEmptyOwner := apiCheck("spaceman_event_subscription.pinned", func(ctx context.Context, c *client.Client, id string, s *terraform.State) error {
		sub, err := c.GetSubscription(ctx, stateAttr(s, "spaceman_event_subscription.pinned", "space_id"), id)
		if err != nil {
			return err
		}
		if v, ok := sub.Vars["owner"]; !ok || v != "" || sub.Vars["repository"] != "acme/api" {
			return fmt.Errorf("stored vars = %v, want repository acme/api and owner \"\"", sub.Vars)
		}
		return nil
	})
	accSteps(t,
		resource.TestStep{
			Config: hcl("a", false),
			Check: resource.ComposeAggregateTestCheckFunc(
				onTemplate("pinned", "a"), onTemplate("defaults", "a"), onTemplate("moving", "a"),
				resource.TestCheckResourceAttr("spaceman_event_subscription.pinned", "vars.%", "1"),
				resource.TestCheckResourceAttr("spaceman_event_subscription.defaults", "vars.%", "2"),
				storedEmptyOwner,
			),
		},
		// Another template's version: template_id changes with it (the apply used to fail "inconsistent result").
		resource.TestStep{
			Config: hcl("b", false),
			Check: resource.ComposeAggregateTestCheckFunc(
				onTemplate("pinned", "b"), onTemplate("defaults", "b"), onTemplate("moving", "b"),
				resource.TestCheckResourceAttr("spaceman_event_subscription.pinned", "vars.%", "1"),
				resource.TestCheckResourceAttr("spaceman_event_subscription.defaults", "vars.%", "2"),
				storedEmptyOwner,
			),
		},
		resource.TestStep{Config: hcl("b", false), PlanOnly: true},
		// A custom definition clears the template and its version.
		resource.TestStep{
			Config: hcl("b", true),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_event_subscription.moving", "template_id", ""),
				resource.TestCheckResourceAttr("spaceman_event_subscription.moving", "template_version_id", ""),
			),
		},
		// And back to a template version.
		resource.TestStep{
			Config: hcl("a", false),
			Check: resource.ComposeAggregateTestCheckFunc(
				onTemplate("pinned", "a"), onTemplate("defaults", "a"), onTemplate("moving", "a"),
				resource.TestCheckNoResourceAttr("spaceman_event_subscription.moving", "custom"),
			),
		},
		resource.TestStep{Config: hcl("a", false), PlanOnly: true},
	)
}
