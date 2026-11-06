package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func gitKeyHCL(slug, nodeID, key, repo string) string {
	return sshRepoHCL(slug, nodeID, repo) + fmt.Sprintf(`
resource "spaceman_git_ssh_key" "k" {
  name   = %q
  spaces = [spaceman_space.s.id]
%s
}
`, slug, key)
}

// TestAccGitSSHKey (SpaceMan ADR-0058): a shared key's public half and fingerprint appear in state (never the private
// key); a repository moves from its own key to the shared one in one apply; bumping key_version rotates the key; the key
// imports by ID.
func TestAccGitSSHKey(t *testing.T) {
	testAccPreCheck(t)
	slug := "tf-gitkey-" + suffix()
	shared := "  ssh_key = false\n  git_key_id = spaceman_git_ssh_key.k.id"
	var first string
	accSteps(t,
		resource.TestStep{
			Config: gitKeyHCL(slug, acc.nodeA.ID, "", "  ssh_key = true"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_git_ssh_key.k", "all_spaces", "false"),
				resource.TestCheckResourceAttr("spaceman_git_ssh_key.k", "spaces.#", "1"),
				resource.TestCheckResourceAttr("spaceman_space_repository.api", "git_key_id", ""),
				func(s *terraform.State) error {
					pub, fp := stateAttr(s, "spaceman_git_ssh_key.k", "public_key"), stateAttr(s, "spaceman_git_ssh_key.k", "fingerprint")
					if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasPrefix(fp, "SHA256:") {
						return fmt.Errorf("key in state: %q %q", pub, fp)
					}
					for _, r := range s.RootModule().Resources {
						for k, v := range r.Primary.Attributes {
							if strings.Contains(v, "PRIVATE KEY") {
								return fmt.Errorf("a private key in state: %s", k)
							}
						}
					}
					first = fp
					return nil
				},
			),
		},
		resource.TestStep{
			Config: gitKeyHCL(slug, acc.nodeA.ID, "", shared),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_space_repository.api", "ssh_key", "false"),
				resource.TestCheckResourceAttrPair("spaceman_space_repository.api", "git_key_id", "spaceman_git_ssh_key.k", "id"),
				apiCheck("spaceman_git_ssh_key.k", func(ctx context.Context, c *client.Client, id string, _ *terraform.State) error {
					k, err := c.GetGitKey(ctx, id)
					if err != nil {
						return err
					}
					if len(k.UsedBy) != 1 {
						return fmt.Errorf("used by %d repositories, want 1", len(k.UsedBy))
					}
					return nil
				}),
			),
		},
		resource.TestStep{Config: gitKeyHCL(slug, acc.nodeA.ID, "", shared), PlanOnly: true},
		resource.TestStep{
			Config: gitKeyHCL(slug, acc.nodeA.ID, "  description = \"rotated\"\n  key_version = 2", shared),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_git_ssh_key.k", "description", "rotated"),
				func(s *terraform.State) error {
					if fp := stateAttr(s, "spaceman_git_ssh_key.k", "fingerprint"); fp == first || fp == "" {
						return fmt.Errorf("key_version did not rotate the key: %q", fp)
					}
					return nil
				},
			),
		},
		resource.TestStep{ResourceName: "spaceman_git_ssh_key.k", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"key_version"}},
	)
}
