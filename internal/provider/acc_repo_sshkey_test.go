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

const testKnownHost = "git.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

func sshRepoHCL(slug, nodeID, key string) string {
	return spaceHCL(slug, "SSH keys", "", nodeID) + fmt.Sprintf(`
resource "spaceman_space_repository" "api" {
  space_id     = spaceman_space.s.id
  name         = "api"
  url          = "git@git.example.com:acme/api.git"
  clone_policy = "manual"
  known_hosts  = %q
%s
}
`, testKnownHost, key)
}

// TestAccSpaceRepositorySSHKey (SpaceMan ADR-0057): ssh_key = true makes SpaceMan generate a key whose public half and
// fingerprint appear in state (never the private key); bumping ssh_key_version rotates it; ssh_key = false removes it;
// known_hosts is managed like any field; importing reads the key's public half.
func TestAccSpaceRepositorySSHKey(t *testing.T) {
	testAccPreCheck(t)
	slug := "tf-sshkey-" + suffix()
	var first string
	accSteps(t,
		resource.TestStep{
			Config: sshRepoHCL(slug, acc.nodeA.ID, "  ssh_key = true"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_space_repository.api", "ssh_key", "true"),
				resource.TestCheckResourceAttr("spaceman_space_repository.api", "known_hosts", testKnownHost),
				func(s *terraform.State) error {
					pub, fp := stateAttr(s, "spaceman_space_repository.api", "ssh_public_key"), stateAttr(s, "spaceman_space_repository.api", "ssh_key_fingerprint")
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
		resource.TestStep{Config: sshRepoHCL(slug, acc.nodeA.ID, "  ssh_key = true"), PlanOnly: true},
		resource.TestStep{
			Config: sshRepoHCL(slug, acc.nodeA.ID, "  ssh_key = true\n  ssh_key_version = 2"),
			Check: func(s *terraform.State) error {
				if fp := stateAttr(s, "spaceman_space_repository.api", "ssh_key_fingerprint"); fp == first || fp == "" {
					return fmt.Errorf("ssh_key_version did not rotate the key: %q", fp)
				}
				return nil
			},
		},
		resource.TestStep{ResourceName: "spaceman_space_repository.api", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"ssh_key_version"},
			ImportStateIdFunc:       func(s *terraform.State) (string, error) { return stateAttr(s, "spaceman_space.s", "id") + "/api", nil }},
		resource.TestStep{
			Config: sshRepoHCL(slug, acc.nodeA.ID, "  ssh_key = false"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_space_repository.api", "ssh_key", "false"),
				resource.TestCheckResourceAttr("spaceman_space_repository.api", "ssh_public_key", ""),
				apiCheck("spaceman_space_repository.api", func(ctx context.Context, c *client.Client, id string, s *terraform.State) error {
					r, err := c.GetSpaceRepository(ctx, stateAttr(s, "spaceman_space.s", "id"), id)
					if err != nil {
						return err
					}
					if r.SSHKey != nil {
						return fmt.Errorf("key still there: %+v", r.SSHKey)
					}
					return nil
				}),
			),
		},
	)
}
