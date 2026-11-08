package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func optionalFiltersHCL(sfx, spaceSlug, nodeID, pulseURL, creds, title string) string {
	return fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %[2]q
  name             = "Optional filters"
  node_id          = %[3]q
  purge_on_destroy = true
}

resource "spaceman_event_connection" "c" {
  name             = "tf-pulse-opt-%[1]s"
  url              = %[4]q
  creds_wo         = %[5]q
  creds_wo_version = 1
}

locals {
  definition = {
    subject = "evt.github.issue_comment.>"
    variables = [
      { name = "repository", description = "owner/name; empty for every repository" },
      { name = "include_bots", pattern = "true" },
    ]
    filters = [
      { path = "data.repository.full_name", op = "eq", value = "{{vars.repository}}", if_set = "repository" },
      { path = "data.actor.type", op = "ne", value = "Bot", unless_set = "include_bots" },
      { path = "data.payload.issue.pull_request", op = "absent" },
    ]
    title = %[6]q
    body  = "{{data.payload.comment.body}}"
  }
}

resource "spaceman_event_template" "t" {
  slug       = "tf-opt-%[1]s"
  name       = "TF optional filters"
  definition = jsonencode(local.definition)
}

resource "spaceman_event_subscription" "custom" {
  space_id      = spaceman_space.s.id
  name          = "custom optional filters"
  connection_id = spaceman_event_connection.c.id
  custom        = jsonencode(local.definition)
  vars          = { repository = "acme/api" }
}

data "spaceman_event_template" "gh" {
  slug = "github-issue-comment"
}
`, sfx, spaceSlug, nodeID, pulseURL, creds, title)
}

// badOpHCL adds a template with an unknown op, and badIfSetHCL a custom subscription whose if_set names an undefined
// variable: both are refused at plan time, before anything reaches SpaceMan.
const badOpHCL = `
resource "spaceman_event_template" "bad_op" {
  slug = "tf-bad-op"
  name = "TF bad op"
  definition = jsonencode({
    subject = "evt.github.>"
    filters = [{ path = "data.action", op = "regex", value = "^op" }]
    title   = "x"
  })
}
`

const badIfSetHCL = `
resource "spaceman_event_subscription" "bad_if_set" {
  space_id      = spaceman_space.s.id
  name          = "bad if_set"
  connection_id = spaceman_event_connection.c.id
  custom = jsonencode({
    subject = "evt.github.>"
    filters = [{ path = "data.action", op = "eq", value = "opened", if_set = "missing" }]
    title   = "x"
  })
}
`

// filtersOf decodes the filters of a definition stored in state.
func filtersOf(s *terraform.State, res, attr string) ([]client.TemplateFilter, error) {
	var d client.TemplateDefinition
	if err := json.Unmarshal([]byte(stateAttr(s, res, attr)), &d); err != nil {
		return nil, fmt.Errorf("%s.%s: %w", res, attr, err)
	}
	return d.Filters, nil
}

// TestAccEventTemplateOptionalFilters (SpaceMan ADR-0061, needs SpaceMan 0.1.0-rc.9 or later): if_set, unless_set and
// the absent op round-trip through a template, a custom subscription and the data source of a built-in.
func TestAccEventTemplateOptionalFilters(t *testing.T) {
	testAccPreCheck(t)
	sfx, slug := suffix(), "tf-opt-"+suffix()
	checkServer := func(s *terraform.State) error {
		for _, ref := range [][2]string{{"spaceman_event_template.t", "definition"}, {"spaceman_event_subscription.custom", "custom"}} {
			f, err := filtersOf(s, ref[0], ref[1])
			if err != nil {
				return err
			}
			if len(f) != 3 || f[0].IfSet != "repository" || f[1].UnlessSet != "include_bots" || f[2].Op != "absent" {
				return fmt.Errorf("%s filters: %+v", ref[0], f)
			}
		}
		f, err := filtersOf(s, "data.spaceman_event_template.gh", "definition")
		if err != nil {
			return err
		}
		var ifSet, unlessSet, absent bool
		for _, x := range f {
			ifSet = ifSet || x.IfSet != ""
			unlessSet = unlessSet || x.UnlessSet != ""
			absent = absent || x.Op == "absent"
		}
		if !ifSet || !unlessSet || !absent {
			return fmt.Errorf("github-issue-comment filters miss if_set/unless_set/absent: %+v", f)
		}
		return nil
	}
	accSteps(t,
		resource.TestStep{
			Config: optionalFiltersHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, "Comment"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_event_template.t", "latest_version", "1"),
				resource.TestCheckResourceAttr("data.spaceman_event_template.gh", "builtin", "true"),
				checkServer,
			),
		},
		resource.TestStep{Config: optionalFiltersHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, "Comment"), PlanOnly: true},
		resource.TestStep{
			Config: optionalFiltersHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, "Comment on an issue"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_event_template.t", "latest_version", "2"),
				checkServer,
			),
		},
		// The plan-time validator (definitionValidator): an unknown op and an if_set naming an undefined variable.
		resource.TestStep{
			Config:      optionalFiltersHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, "Comment on an issue") + badOpHCL,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`"regex"\s+is\s+not\s+one\s+of`),
		},
		resource.TestStep{
			Config:      optionalFiltersHCL(sfx, slug, acc.nodeA.ID, acc.pulseURL, acc.creds, "Comment on an issue") + badIfSetHCL,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`variable\s+"missing"\s+is\s+not\s+defined`),
		},
		resource.TestStep{ResourceName: "spaceman_event_template.t", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "spaceman_event_subscription.custom", ImportState: true, ImportStateVerify: true,
			// An import reads every variable, including the defaults SpaceMan filled in (not drift in a plan).
			ImportStateVerifyIgnore: []string{"vars"},
			ImportStateIdFunc: func(s *terraform.State) (string, error) {
				return stateAttr(s, "spaceman_event_subscription.custom", "space_id") + "/" + stateAttr(s, "spaceman_event_subscription.custom", "id"), nil
			}},
	)
}
