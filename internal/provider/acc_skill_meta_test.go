package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func skillMetaHCL(sfx, name, desc, text string) string {
	return fmt.Sprintf(`
resource "spaceman_skill" "k" {
  slug        = "tf-skmeta-%[1]s"
  name        = %[2]q
  description = %[3]q
  files = {
    "SKILL.md" = "---\nname: tf-skmeta-%[1]s\ndescription: A test skill.\n---\n\n%[4]s\n"
  }
}
`, sfx, name, desc, text)
}

// skillVersions checks how many published versions the skill has through the API.
func skillVersions(res string, want int) resource.TestCheckFunc {
	return apiCheck(res, func(ctx context.Context, c *client.Client, id string, _ *terraform.State) error {
		s, err := c.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		n := 0
		for _, v := range s.Versions {
			if v.State == "published" {
				n++
			}
		}
		if n != want {
			return fmt.Errorf("skill has %d published versions, want %d", n, want)
		}
		return nil
	})
}

// A skill's name and description change in place and publish nothing; a file change still publishes.
func TestAccSkillMetadataInPlace(t *testing.T) {
	testAccPreCheck(t)
	sfx := suffix()
	const k = "spaceman_skill.k"
	var id string
	accSteps(t,
		resource.TestStep{
			Config: skillMetaHCL(sfx, "Name one", "Desc one", "Body one."),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(k, "latest_version", "1"),
				skillVersions(k, 1),
				func(s *terraform.State) error { id = stateAttr(s, k, "id"); return nil },
			),
		},
		// Only name and description: the version fields are pinned in the plan and no version is created.
		resource.TestStep{
			Config: skillMetaHCL(sfx, "Name two", "Desc two", "Body one."),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(k, plancheck.ResourceActionUpdate),
				plancheck.ExpectKnownValue(k, tfjsonpath.New("latest_version"), knownvalue.Int64Exact(1)),
				plancheck.ExpectKnownValue(k, tfjsonpath.New("sha256"), knownvalue.NotNull()),
				plancheck.ExpectKnownValue(k, tfjsonpath.New("latest_version_id"), knownvalue.NotNull()),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(k, "name", "Name two"),
				resource.TestCheckResourceAttr(k, "description", "Desc two"),
				resource.TestCheckResourceAttr(k, "latest_version", "1"),
				skillVersions(k, 1),
				func(s *terraform.State) error {
					if got := stateAttr(s, k, "id"); got != id {
						return fmt.Errorf("the skill was replaced: id %s -> %s", id, got)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: skillMetaHCL(sfx, "Name two", "Desc two", "Body one."), PlanOnly: true},
		// A file change publishes the next version (and the plan marks the version fields unknown).
		resource.TestStep{
			Config: skillMetaHCL(sfx, "Name two", "Desc two", "Body two."),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectUnknownValue(k, tfjsonpath.New("latest_version"))}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(k, "latest_version", "2"),
				skillVersions(k, 2),
			),
		},
		// Both at once: metadata changes and a version is published.
		resource.TestStep{
			Config: skillMetaHCL(sfx, "Name three", "", "Body three."),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(k, "name", "Name three"),
				resource.TestCheckResourceAttr(k, "description", ""),
				resource.TestCheckResourceAttr(k, "latest_version", "3"),
				skillVersions(k, 3),
			),
		},
		resource.TestStep{ResourceName: k, ImportState: true, ImportStateVerify: true},
	)
}
