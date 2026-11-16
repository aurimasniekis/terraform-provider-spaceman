package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hpAdminPermissions are what the harborpilot provider's own acceptance tests grant their Terraform principal.
var hpAdminPermissions = []string{"config.read", "config.write", "policy.publish", "secrets.write", "tokens.manage", "usage.read",
	"activity.read", "events.manage", "notifications.manage", "compat.manage", "audit.read", "users.manage", "auth.settings", "system.manage"}

const harborPilotProviderModule = "github.com/aurimasniekis/terraform-provider-harbor-pilot"

// harborPilotProviderVer is the harborpilot provider the combined example is tested with (the first with ephemeral
// service-principal tokens and revoke_on_close).
const harborPilotProviderVer = "v0.1.1"

// buildProviders builds this provider and the harborpilot provider into one directory and writes a Terraform CLI
// configuration whose dev_overrides point both at it (no registry needed). HARBORPILOT_PROVIDER_SRC may name a
// checkout of terraform-provider-harbor-pilot; otherwise the pinned release is fetched with `go mod download`.
func buildProviders(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	src := os.Getenv("HARBORPILOT_PROVIDER_SRC")
	if src == "" {
		dl := exec.Command("go", "mod", "download", "-json", harborPilotProviderModule+"@"+harborPilotProviderVer) //nolint:gosec // pinned module
		dl.Dir = t.TempDir()
		dl.Env = append(cleanEnv(), "GOFLAGS=-mod=mod")
		out, err := dl.Output()
		if err != nil {
			t.Fatalf("go mod download %s@%s: %v", harborPilotProviderModule, harborPilotProviderVer, err)
		}
		var m struct{ Dir string }
		if err := json.Unmarshal(out, &m); err != nil || m.Dir == "" {
			t.Fatalf("go mod download: no module directory (%v)", err)
		}
		src = m.Dir
	}
	for _, b := range []struct{ dir, out string }{{root, "terraform-provider-spaceman"}, {src, "terraform-provider-harbor-pilot"}} {
		cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, b.out), ".") //nolint:gosec // test builds
		cmd.Dir = b.dir
		cmd.Env = cleanEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", b.out, err, out)
		}
	}
	rc := filepath.Join(dir, "terraformrc")
	cfg := fmt.Sprintf("provider_installation {\n  dev_overrides {\n    \"aurimasniekis/spaceman\" = %[1]q\n    \"aurimasniekis/harbor-pilot\" = %[1]q\n  }\n  direct {}\n}\n", dir)
	if err := os.WriteFile(rc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return rc
}

// TestAccHarborPilotExample applies examples/harbor-pilot against SpaceMan and a real `harbor-pilot serve --demo` with
// the harborpilot provider, plans again (empty), checks that no Harbor Pilot token is in the state, and destroys.
func TestAccHarborPilotExample(t *testing.T) {
	hpPreCheck(t)
	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform is not on PATH")
	}
	rc := buildProviders(t)
	g := startHarborPilot(t)
	ctx := context.Background()
	adminToken, err := g.admin.serviceToken(ctx, "terraform-example", hpAdminPermissions)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	main, err := os.ReadFile(filepath.Join("..", "..", "examples", "harbor-pilot", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "main.tf"), main, 0o600); err != nil {
		t.Fatal(err)
	}
	prefix := "tfhp" + suffix()
	env := append(cleanEnv(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1",
		EnvEndpoint+"="+acc.endpoint, EnvToken+"="+acc.token, "HP_SERVER="+g.URL, "HP_API_TOKEN="+adminToken,
		"TF_VAR_pulse_creds="+acc.creds)
	vars := []string{"-var", "prefix=" + prefix, "-var", "node_name=" + acc.nodeA.Name, "-var", "harbor_pilot_url=" + g.URL,
		"-var", "allow_insecure_http=true", "-var", "pulse_url=" + acc.pulseURL}
	run := func(args ...string) (int, string) {
		cmd := exec.Command(tf, append(args, vars...)...) //nolint:gosec // the test's terraform
		cmd.Dir, cmd.Env = work, env
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0, out.String()
		case errors.As(err, &ee):
			return ee.ExitCode(), out.String()
		default:
			t.Fatalf("terraform %s: %v", args[0], err)
			return -1, ""
		}
	}
	scrub := func(s string) string {
		for _, tok := range []string{adminToken, acc.token} {
			s = strings.ReplaceAll(s, tok, "<redacted>")
		}
		return s
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			if code, out := run("destroy", "-auto-approve", "-input=false", "-no-color"); code != 0 {
				t.Logf("cleanup destroy failed (%d):\n%s", code, scrub(out))
			}
		}
	})

	code, out := run("apply", "-auto-approve", "-input=false", "-no-color")
	if code != 0 {
		t.Fatalf("apply failed (%d):\n%s", code, scrub(out))
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Apply complete!") {
			t.Log(l)
		}
	}
	// A second plan is empty, although the ephemeral resources issue new tokens on every run.
	if code, out := run("plan", "-detailed-exitcode", "-input=false", "-no-color"); code != 0 {
		t.Fatalf("second plan is not empty (exit %d):\n%s", code, scrub(out))
	}
	state, err := os.ReadFile(filepath.Join(work, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"hpk_", "hpa_", adminToken} {
		if bytes.Contains(state, []byte(p)) {
			t.Fatalf("a Harbor Pilot token (%s…) is in the state", p[:4])
		}
	}
	// SpaceMan holds the connection and both spaces' settings, each on its own project.
	conn, err := acc.client.GetHarborPilotConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !conn.HasParentToken || !conn.HasServiceToken || conn.ParentTokenVersion != 1 || len(conn.EnabledSpaces) != 2 {
		t.Fatalf("connection = has_parent %v, has_service %v, parent_version %d, enabled spaces %v", conn.HasParentToken,
			conn.HasServiceToken, conn.ParentTokenVersion, conn.EnabledSpaces)
	}
	var st struct {
		Resources []struct {
			Type      string `json:"type"`
			Instances []struct {
				IndexKey   any            `json:"index_key"`
				Attributes map[string]any `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(state, &st); err != nil {
		t.Fatal(err)
	}
	projects := map[string]string{}
	for _, r := range st.Resources {
		if r.Type == "harborpilot_project" {
			for _, in := range r.Instances {
				projects[fmt.Sprint(in.IndexKey)] = fmt.Sprint(in.Attributes["id"])
			}
		}
	}
	settings := 0
	for _, r := range st.Resources {
		if r.Type != "spaceman_space_harbor_pilot" {
			continue
		}
		for _, in := range r.Instances {
			s, err := acc.client.GetSpaceHarborPilot(ctx, fmt.Sprint(in.Attributes["space_id"]))
			if err != nil {
				t.Fatal(err)
			}
			if !s.Enabled || s.ProjectID != projects[fmt.Sprint(in.IndexKey)] || s.ProjectID == "" {
				t.Fatalf("space %v: enabled %v, project %q, want %q", in.IndexKey, s.Enabled, s.ProjectID, projects[fmt.Sprint(in.IndexKey)])
			}
			settings++
		}
	}
	if settings != 2 {
		t.Fatalf("%d spaces with Harbor Pilot settings, want 2", settings)
	}

	if code, out := run("destroy", "-auto-approve", "-input=false", "-no-color"); code != 0 {
		t.Fatalf("destroy failed (%d):\n%s", code, scrub(out))
	}
	destroyed = true
	if _, err := acc.client.GetHarborPilotConnection(ctx); err == nil {
		t.Fatal("the Harbor Pilot connection is still there after destroy")
	}
}
