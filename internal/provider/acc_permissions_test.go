package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// permissionsHCL is a space, a custom type and (with perms) a permission on approvals and one on the custom type.
func permissionsHCL(sfx, slug, nodeID, approval string, perms bool, extra string) string {
	s := fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %[2]q
  name             = "Permissions"
  node_id          = %[3]q
  purge_on_destroy = true
}

resource "spaceman_human_action_type" "t" {
  slug           = "tf-perm-%[1]s"
  name           = "Deploy window"
  request_schema = jsonencode({ type = "object" })
}
`, sfx, slug, nodeID)
	if perms {
		s += fmt.Sprintf(`
resource "spaceman_human_action_type_permission" "a" {
  space_id    = spaceman_space.s.id
  action_type = "approval"
%s
}

resource "spaceman_human_action_type_permission" "c" {
  space_id    = spaceman_space.s.id
  action_type = spaceman_human_action_type.t.id
  create_role = "admin"
  agents      = "none"
}
`, approval)
	}
	return s + extra
}

func permissionOf(t *testing.T, slug, typ string) *client.HumanTypePermission {
	t.Helper()
	p, err := acc.client.GetHumanTypePermission(context.Background(), spaceIDBySlug(t, slug), typ)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAccHumanTypePermissions covers spaceman_human_action_type_permission (SM-HUI-008, ADR-0050): create on a built-in
// and a custom type (by ID) with unset fields from the defaults, plan stability, in-place update (agent_roles follow
// agents), import, drift of a configured field and none of an unconfigured one, an existing permission refused on
// create, and destroy restoring the defaults.
func TestAccHumanTypePermissions(t *testing.T) {
	testAccPreCheck(t)
	sfx, slug := suffix(), "tf-perm-"+suffix()
	a1 := "  view_role    = \"operator\"\n  respond_role = \"admin\"\n  agents       = \"roles\"\n  agent_roles  = [\"deployer\", \"reviewer\"]"
	a2 := "  view_role    = \"admin\"\n  respond_role = \"admin\"\n  agents       = \"any\""
	accSteps(t,
		resource.TestStep{
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a1, true, ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "view_role", "operator"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "create_role", "operator"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "agent_roles.#", "2"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "type_slug", ""),
				resource.TestCheckResourceAttrPair("spaceman_human_action_type_permission.c", "action_type", "spaceman_human_action_type.t", "id"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.c", "type_slug", "tf-perm-"+sfx),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.c", "view_role", "viewer"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.c", "agent_roles.#", "0"),
				func(s *terraform.State) error {
					if p := permissionOf(t, slug, "approval"); !p.Stored || p.Agents != "roles" || p.RespondRole != "admin" {
						return fmt.Errorf("stored approval permission %+v", p)
					}
					if id := stateAttr(s, "spaceman_human_action_type_permission.a", "id"); id != spaceIDBySlug(t, slug)+"/approval" {
						return fmt.Errorf("id %q", id)
					}
					return nil
				},
			),
		},
		resource.TestStep{Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a1, true, ""), PlanOnly: true},
		// In place: agents any clears the roles even though agent_roles is no longer configured.
		resource.TestStep{
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a2, true, ""),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_human_action_type_permission.a", plancheck.ResourceActionUpdate)}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "view_role", "admin"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "agents", "any"),
				resource.TestCheckResourceAttr("spaceman_human_action_type_permission.a", "agent_roles.#", "0"),
			),
		},
		resource.TestStep{Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a2, true, ""), PlanOnly: true},
		resource.TestStep{ResourceName: "spaceman_human_action_type_permission.a", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "spaceman_human_action_type_permission.c", ImportState: true, ImportStateVerify: true},
		// An unconfigured field edited through the API is not drift; a configured one is, and applying restores it.
		resource.TestStep{
			PreConfig: func() {
				editPermission(t, slug, "approval", func(in *client.HumanTypePermissionInput) { in.CreateRole = "admin" })
			},
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a2, true, ""), PlanOnly: true,
		},
		resource.TestStep{
			PreConfig: func() {
				editPermission(t, slug, "approval", func(in *client.HumanTypePermissionInput) { in.ViewRole = "viewer" })
			},
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a2, true, ""), PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a2, true, ""),
			Check: func(*terraform.State) error {
				if p := permissionOf(t, slug, "approval"); p.ViewRole != "admin" || p.CreateRole != "admin" {
					return fmt.Errorf("after correcting drift %+v", p)
				}
				return nil
			},
		},
		// A permission stored outside Terraform is not overwritten: create fails (import it instead).
		resource.TestStep{
			PreConfig: func() {
				if _, err := acc.client.SetHumanTypePermission(context.Background(), spaceIDBySlug(t, slug), "todo", 0,
					client.HumanTypePermissionInput{CreateRole: "admin"}); err != nil {
					t.Fatal(err)
				}
			},
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, a2, true, `
resource "spaceman_human_action_type_permission" "todo" {
  space_id    = spaceman_space.s.id
  action_type = "todo"
}
`),
			ExpectError: regexp.MustCompile(`The type permission already exists`),
		},
		// Destroying the resources deletes the permissions: the defaults apply again.
		resource.TestStep{
			Config: permissionsHCL(sfx, slug, acc.nodeA.ID, "", false, ""),
			Check: func(s *terraform.State) error {
				for _, typ := range []string{"approval", stateAttr(s, "spaceman_human_action_type.t", "id")} {
					if p := permissionOf(t, slug, typ); p.Stored || p.ViewRole != "viewer" || p.Agents != "any" {
						return fmt.Errorf("%s after destroy %+v", typ, p)
					}
				}
				if p := permissionOf(t, slug, "todo"); !p.Stored {
					return fmt.Errorf("a permission Terraform did not own was removed: %+v", p)
				}
				return nil
			},
		},
	)
}

func editPermission(t *testing.T, slug, typ string, edit func(*client.HumanTypePermissionInput)) {
	t.Helper()
	ctx := context.Background()
	id := spaceIDBySlug(t, slug)
	p, err := acc.client.GetHumanTypePermission(ctx, id, typ)
	if err != nil {
		t.Fatal(err)
	}
	in := client.HumanTypePermissionInput{ViewRole: p.ViewRole, CreateRole: p.CreateRole, RespondRole: p.RespondRole, Agents: p.Agents, AgentRoles: p.AgentRoles}
	edit(&in)
	if _, err := acc.client.SetHumanTypePermission(ctx, id, typ, p.Version, in); err != nil {
		t.Fatal(err)
	}
}
