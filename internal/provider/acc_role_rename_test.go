package provider

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// centralSQL runs a statement against the harness's central database. The harness has no node agent, so the node
// side of a rename (protocol 1.14, the memory move acknowledged) is recorded directly, as a connected agent would.
func centralSQL(t *testing.T, stmt string) {
	t.Helper()
	db := filepath.Join(acc.dir, "central", "spaceman.db")
	out, err := exec.Command("python3", "-I", "-c",
		"import sqlite3,sys; c=sqlite3.connect(sys.argv[1], timeout=30); c.execute(sys.argv[2]); c.commit()", db, stmt).CombinedOutput()
	if err != nil {
		t.Skipf("cannot run %q against %s: %v\n%s", stmt, db, err, out)
	}
}

// nodeSpeaksRoleRenameProtocol: central refuses a rename in a space whose node did not negotiate node protocol 1.14.
func nodeSpeaksRoleRenameProtocol(t *testing.T) { centralSQL(t, "UPDATE nodes SET protocol_minor=14") }

// nodeMovedRoleMemory: a pending memory move blocks the next rename of the role, so the node acknowledges it.
func nodeMovedRoleMemory(t *testing.T) {
	centralSQL(t, "UPDATE space_role_renames SET node_state='done'")
}

// roleRenameAccHCL is a role with a dependent of every kind that references it by name. `addr` is the role's resource
// address, `name` its name, `keeper` the name of the knowledge base's keeper role.
func roleRenameAccHCL(sfx, slug, nodeID, addr, name, keeper string) string {
	return fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %[2]q
  name             = "Rename"
  node_id          = %[3]q
  purge_on_destroy = true
}

resource "spaceman_role_template" "w" {
  slug = "tf-rn-w-%[1]s"
  name = "Worker"
  kind = "worker"
}

resource "spaceman_role_template" "kb" {
  slug = "tf-rn-kb-%[1]s"
  name = "KB keeper"
  kind = "kb"
}

resource "spaceman_role_template" "dreamer" {
  slug = "tf-rn-d-%[1]s"
  name = "Dreamer"
  kind = "dreamer"
}

resource "spaceman_space_role" "%[4]s" {
  space_id     = spaceman_space.s.id
  name         = %[5]q
  template_id  = spaceman_role_template.w.id
  instructions = "Work in small steps."
}

resource "spaceman_space_role" "keeper" {
  space_id    = spaceman_space.s.id
  name        = %[6]q
  template_id = spaceman_role_template.kb.id
}

resource "spaceman_space_role" "dreamer" {
  space_id    = spaceman_space.s.id
  name        = "dreamer"
  template_id = spaceman_role_template.dreamer.id
}

resource "spaceman_schedule" "s" {
  space_id         = spaceman_space.s.id
  name             = "rotate-logs"
  kind             = "interval"
  interval_seconds = 3600
  anchor           = "2030-01-01T00:00:00Z"
  target           = "task"
  title            = "Rotate the logs"
  assignee_role    = spaceman_space_role.%[4]s.name
}

resource "spaceman_knowledge_base" "k" {
  space_id   = spaceman_space.s.id
  slug       = "docs"
  name       = "Docs"
  repo       = "kb/docs"
  space_role = spaceman_space_role.keeper.name
  validator  = ["python3", "tools/kb.py", "check"]

  allowed_roles = [spaceman_space_role.%[4]s.name]
}

resource "spaceman_dream" "d" {
  space_id         = spaceman_space.s.id
  slug             = "nightly"
  name             = "Nightly"
  space_role       = spaceman_space_role.dreamer.name
  memory_roles     = [spaceman_space_role.%[4]s.name]
  transcript_roles = [spaceman_space_role.%[4]s.name]
  interval_hours   = 24
}

resource "spaceman_human_action_type_permission" "p" {
  space_id    = spaceman_space.s.id
  action_type = "question"
  agents      = "roles"
  agent_roles = [spaceman_space_role.%[4]s.name]
}

resource "spaceman_human_action_policy" "q" {
  space_id       = spaceman_space.s.id
  action_type    = "question"
  successor_role = spaceman_space_role.%[4]s.name
}

resource "spaceman_mcp_server" "m" {
  slug          = "tfrn%[1]s"
  name          = "Tracker"
  url           = "http://127.0.0.1:9/mcp"
  allow_private = true
}

resource "spaceman_mcp_grant" "g" {
  server_id = spaceman_mcp_server.m.id
  space_id  = spaceman_space.s.id
  role      = spaceman_space_role.%[4]s.name
}
resource "spaceman_account" "a" {
  name              = "tf-rn-%[1]s"
  kind              = "anthropic_api_key"
  secret_wo         = "sk-test-%[1]s"
  secret_wo_version = 1
}

resource "spaceman_space_accounts" "b" {
  space_id = spaceman_space.s.id
  binding = [{
    account_id = spaceman_account.a.id
    default    = true
  }]
}

resource "spaceman_space_role_pool" "p" {
  space_id   = spaceman_space.s.id
  role       = spaceman_space_role.%[4]s.name
  account    = [{ account_id = spaceman_account.a.id }]
  depends_on = [spaceman_space_accounts.b]
}
`, sfx, slug, nodeID, addr, name, keeper)
}

// TestAccSpaceRoleRename covers renaming a role in place (SpaceMan 0.1.11 or later): dependents that reference it by
// name follow in the same apply (the grant and the pool, which hold ids and state of their own, are updated in place and
// survive with the same ids), the role is never destroyed, a rename made outside Terraform is an in-place name change
// back, and renaming the resource address as well plans no destroy.
func TestAccSpaceRoleRename(t *testing.T) {
	testAccPreCheck(t)
	nodeSpeaksRoleRenameProtocol(t)
	sfx, slug := suffix(), "tf-rn-"+suffix()
	const r = "spaceman_space_role.implementor"
	var spaceID, grantID string
	var revision int64
	noDestroy := func(addrs ...string) resource.ConfigPlanChecks {
		var cs []plancheck.PlanCheck
		for _, a := range addrs {
			cs = append(cs, plancheck.ExpectResourceAction(a, plancheck.ResourceActionUpdate))
		}
		return resource.ConfigPlanChecks{PreApply: cs}
	}
	roleIs := func(name string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			sr, err := acc.client.GetSpaceRole(context.Background(), spaceID, name)
			if err != nil {
				return err
			}
			if sr.Revision <= revision {
				return fmt.Errorf("revision %d did not grow past %d: the role was re-created", sr.Revision, revision)
			}
			revision = sr.Revision
			return nil
		}
	}
	dependentsFollow := func(name string) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("spaceman_schedule.s", "assignee_role", name),
			resource.TestCheckResourceAttr("spaceman_dream.d", "memory_roles.0", name),
			resource.TestCheckResourceAttr("spaceman_dream.d", "transcript_roles.0", name),
			resource.TestCheckResourceAttr("spaceman_human_action_type_permission.p", "agent_roles.0", name),
			resource.TestCheckResourceAttr("spaceman_human_action_policy.q", "successor_role", name),
			resource.TestCheckResourceAttr("spaceman_mcp_grant.g", "role", name),
			resource.TestCheckResourceAttr("spaceman_space_role_pool.p", "role", name),
			resource.TestCheckResourceAttr("spaceman_knowledge_base.k", "allowed_roles.0", name),
		)
	}
	// The grant and the pool survive a rename: the grant has the same id (not a revoked grant and a new one), the pool is
	// there under the role's new name (its id is `<space_id>/<role>`, so it follows the name).
	survive := func(s *terraform.State) error {
		if got := stateAttr(s, "spaceman_mcp_grant.g", "id"); got != grantID {
			return fmt.Errorf("grant id = %s, want the original %s: it was replaced", got, grantID)
		}
		if _, err := acc.client.GetMCPGrant(context.Background(), stateAttr(s, "spaceman_mcp_grant.g", "server_id"), grantID); err != nil {
			return fmt.Errorf("the grant did not survive: %w", err)
		}
		role := stateAttr(s, "spaceman_space_role_pool.p", "role")
		if got := stateAttr(s, "spaceman_space_role_pool.p", "id"); got != spaceID+"/"+role {
			return fmt.Errorf("pool id = %s, want %s/%s", got, spaceID, role)
		}
		pools, _, err := acc.client.RolePools(context.Background(), spaceID)
		if err != nil {
			return err
		}
		if _, ok := rolePoolOf(pools, role); !ok {
			return fmt.Errorf("role %s has no pool: %v", role, pools)
		}
		return nil
	}
	hclK := func(addr, name, keeper string) string {
		return roleRenameAccHCL(sfx, slug, acc.nodeA.ID, addr, name, keeper)
	}
	hcl := func(addr, name string) string { return hclK(addr, name, "keeper") }
	accSteps(t,
		resource.TestStep{
			Config: hcl("implementor", "implementor"),
			Check: resource.ComposeAggregateTestCheckFunc(
				func(s *terraform.State) error {
					spaceID = stateAttr(s, "spaceman_space.s", "id")
					sr, err := acc.client.GetSpaceRole(context.Background(), spaceID, "implementor")
					if err == nil {
						revision = sr.Revision
					}
					return err
				},
				func(s *terraform.State) error {
					grantID = stateAttr(s, "spaceman_mcp_grant.g", "id")
					return nil
				},
				dependentsFollow("implementor")),
		},
		// Renamed in one apply: the role and its dependents update in place (the grant, which cannot be edited, is
		// replaced). The role's revision keeps growing, so it was never destroyed and created again.
		resource.TestStep{
			Config: hcl("implementor", "implementor-hard"),
			ConfigPlanChecks: noDestroy(r, "spaceman_schedule.s", "spaceman_dream.d", "spaceman_human_action_type_permission.p", "spaceman_human_action_policy.q",
				"spaceman_mcp_grant.g", "spaceman_space_role_pool.p", "spaceman_knowledge_base.k"),
			Check: resource.ComposeAggregateTestCheckFunc(
				func(s *terraform.State) error {
					return resource.TestCheckResourceAttr(r, "id", spaceID+"/implementor-hard")(s)
				},
				roleIs("implementor-hard"), dependentsFollow("implementor-hard"), survive),
		},
		resource.TestStep{Config: hcl("implementor", "implementor-hard"), PlanOnly: true},
		// Renamed outside Terraform: an in-place name change back to the configured value, never a create.
		resource.TestStep{
			PreConfig: func() {
				nodeMovedRoleMemory(t)
				defer nodeMovedRoleMemory(t)
				if _, err := acc.client.RenameSpaceRole(context.Background(), spaceID, "implementor-hard", "elsewhere"); err != nil {
					t.Fatal(err)
				}
			},
			Config:           hcl("implementor", "implementor-hard"),
			ConfigPlanChecks: noDestroy(r),
			Check:            resource.ComposeAggregateTestCheckFunc(roleIs("implementor-hard"), dependentsFollow("implementor-hard"), survive),
		},
		resource.TestStep{Config: hcl("implementor", "implementor-hard"), PlanOnly: true},
		// The KB's keeper is renamed: `space_role` follows in place, and the KB keeps its id.
		resource.TestStep{
			PreConfig:        func() { nodeMovedRoleMemory(t) },
			Config:           hclK("implementor", "implementor-hard", "keeper-2"),
			ConfigPlanChecks: noDestroy("spaceman_space_role.keeper", "spaceman_knowledge_base.k"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_knowledge_base.k", "space_role", "keeper-2"),
				resource.TestCheckResourceAttr("spaceman_knowledge_base.k", "allowed_roles.0", "implementor-hard"), survive),
		},
		resource.TestStep{Config: hclK("implementor", "implementor-hard", "keeper-2"), PlanOnly: true},
		// Renaming the resource address as well (a `moved` block) plans no destroy.
		resource.TestStep{
			Config: hclK("implementor_hard", "implementor-hard", "keeper-2") + `
moved {
  from = spaceman_space_role.implementor
  to   = spaceman_space_role.implementor_hard
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_space_role.implementor_hard", plancheck.ResourceActionNoop)}},
		},
	)
}
