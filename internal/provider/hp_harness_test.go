package provider

// A real Harbor Pilot gateway for the Harbor Pilot acceptance tests: `harbor-pilot serve --demo` (v0.1.3) on a free
// loopback port with its own data directory. It is opt-in, like go-spaceman's hptest: SPACEMAN_HARBOR_PILOT=1, with the
// binary at HARBOR_PILOT_BIN or else the pinned release installed with `go install` into the user cache directory. The
// demo's upstreams are synthetic, so no provider credentials are involved. Tokens are generated at test time and exist
// only in the gateway's temporary data directory; tests never print them.

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	envHarborPilot    = "SPACEMAN_HARBOR_PILOT"
	envHarborPilotBin = "HARBOR_PILOT_BIN"
	harborPilotModule = "github.com/aurimasniekis/go-harbor-pilot/cmd/harbor-pilot"
	harborPilotVer    = "v0.1.3"

	// The demo seed's public administrator (Harbor Pilot internal/demo/demo.go): it exists only in a demo database.
	hpDemoAdminEmail    = "admin@example.test"
	hpDemoAdminPassword = "harbor-pilot-demo" //nolint:gosec // Harbor Pilot's documented, public demo credential
)

// hpGateway is a running, provisioned demo gateway.
type hpGateway struct {
	URL          string
	BindingID    string
	ProjectID    string // the binding's default project (demo "web-app")
	AltProjectID string // a second allowed project (demo "data-pipeline")
	ServiceToken string // hpa_…: usage.read, config.read
	ParentTokens []string
	admin        *hpAdmin
}

func hpPreCheck(t *testing.T) {
	t.Helper()
	testAccPreCheck(t)
	if os.Getenv(envHarborPilot) != "1" {
		t.Skipf("set %s=1 (and optionally %s) to run against a real harbor-pilot serve --demo %s", envHarborPilot, envHarborPilotBin, harborPilotVer)
	}
}

// hpBinary returns HARBOR_PILOT_BIN, or the pinned release from the user cache (installing it once).
func hpBinary(t *testing.T) string {
	t.Helper()
	if b := os.Getenv(envHarborPilotBin); b != "" {
		return b
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "spaceman", "hptest", harborPilotVer)
	bin := filepath.Join(dir, "harbor-pilot")
	pinned := func() bool {
		info, err := buildinfo.ReadFile(bin)
		return err == nil && info.Path == harborPilotModule && info.Main.Version == harborPilotVer
	}
	if pinned() {
		return bin
	}
	cmd := exec.Command("go", "install", harborPilotModule+"@"+harborPilotVer) //nolint:gosec // pinned module and version
	cmd.Env = append(cleanEnv(), "GOBIN="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go install %s@%s: %v\n%s", harborPilotModule, harborPilotVer, err, out)
	}
	if !pinned() {
		t.Fatalf("%s is not %s %s", bin, harborPilotModule, harborPilotVer)
	}
	return bin
}

// startHarborPilot runs a demo gateway for t; the test's cleanup stops it before its directory is removed.
func startHarborPilot(t *testing.T) *hpGateway {
	t.Helper()
	bin := hpBinary(t)
	dir := t.TempDir()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	logf, err := os.Create(filepath.Join(dir, "serve.log")) //nolint:gosec // test-owned temp dir
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "serve", "--demo", "--listen", addr, "--data-dir", filepath.Join(dir, "data")) //nolint:gosec // test binary
	cmd.Env = cleanEnv()
	setGatewayProcAttr(cmd)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		_ = logf.Close()
	})
	base := "http://" + addr
	if !waitFor(60*time.Second, func() bool {
		r, err := http.Get(base + "/readyz") //nolint:noctx // readiness probe of the local test gateway
		if err != nil {
			return false
		}
		_ = r.Body.Close()
		return r.StatusCode == http.StatusOK
	}) {
		b, _ := os.ReadFile(filepath.Join(dir, "serve.log")) //nolint:gosec // test-owned temp dir
		t.Fatalf("harbor-pilot did not become ready:\n%s", tail(string(b), 4096))
	}
	g := &hpGateway{URL: base}
	if err := g.provision(context.Background()); err != nil {
		t.Fatalf("provision harbor-pilot: %v", err)
	}
	return g
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// hpAdmin is the demo administrator's control-plane session (cookie and CSRF token).
type hpAdmin struct {
	base string
	hc   *http.Client
	csrf string
}

func (a *hpAdmin) do(ctx context.Context, method, path string, body, out any, want int) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", a.base)
	if a.csrf != "" {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != want {
		// Error bodies carry a message, never a token; still, print only the message fields.
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("%s %s: status %d: %s%s", method, path, resp.StatusCode, e.Error.Message, e.Detail)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (a *hpAdmin) lookup(ctx context.Context, path, name string) (string, error) {
	var l struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := a.do(ctx, http.MethodGet, path, nil, &l, http.StatusOK); err != nil {
		return "", err
	}
	for _, it := range l.Items {
		if it.Name == name {
			return it.ID, nil
		}
	}
	return "", fmt.Errorf("%s: no %q (not the %s demo seed?)", path, name, harborPilotVer)
}

func signInHarborPilot(ctx context.Context, base string) (*hpAdmin, error) {
	jar, _ := cookiejar.New(nil)
	a := &hpAdmin{base: base, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	var sess struct {
		CSRF string `json:"csrf_token"`
	}
	if err := a.do(ctx, http.MethodPost, "/api/v1/session", map[string]string{"email": hpDemoAdminEmail, "password": hpDemoAdminPassword}, &sess, http.StatusOK); err != nil {
		return nil, fmt.Errorf("sign in: %w", err)
	}
	a.csrf = sess.CSRF
	return a, nil
}

// serviceToken creates a service principal with perms and returns an API token (hpa_…) for it.
func (a *hpAdmin) serviceToken(ctx context.Context, name string, perms []string) (string, error) {
	var sp struct {
		ID string `json:"id"`
	}
	if err := a.do(ctx, http.MethodPost, "/api/v1/service-principals", map[string]any{"name": name, "permissions": perms}, &sp, http.StatusCreated); err != nil {
		return "", fmt.Errorf("service principal: %w", err)
	}
	var tok struct {
		Plaintext string `json:"plaintext"`
	}
	if err := a.do(ctx, http.MethodPost, "/api/v1/service-principals/"+sp.ID+"/tokens", map[string]string{"name": name}, &tok, http.StatusCreated); err != nil {
		return "", fmt.Errorf("service principal token: %w", err)
	}
	if !strings.HasPrefix(tok.Plaintext, "hpa_") {
		return "", fmt.Errorf("unexpected service token format")
	}
	return tok.Plaintext, nil
}

// provision creates a binding on two demo projects that pins only spaceman.fleet, a read-only service principal and two
// parent credentials with tokens:mint (two, so a test can rotate).
func (g *hpGateway) provision(ctx context.Context) error {
	a, err := signInHarborPilot(ctx, g.URL)
	if err != nil {
		return err
	}
	g.admin = a
	plan, err := a.lookup(ctx, "/api/v1/routing-plans", "team-default")
	if err != nil {
		return err
	}
	if g.ProjectID, err = a.lookup(ctx, "/api/v1/projects", "web-app"); err != nil {
		return err
	}
	if g.AltProjectID, err = a.lookup(ctx, "/api/v1/projects", "data-pipeline"); err != nil {
		return err
	}
	var o struct {
		ID string `json:"id"`
	}
	if err := a.do(ctx, http.MethodPost, "/api/v1/client-bindings", map[string]any{"name": "spaceman-tf-test", "spec": map[string]any{
		"display_name": "SpaceMan Terraform test", "owner_kind": "none", "owner_id": "", "plan_id": plan,
		"project_id": g.ProjectID, "allowed_project_ids": []string{g.ProjectID, g.AltProjectID},
		"expected_harness": "", "allowed_models": []string{},
		"limits":        map[string]any{"requests_per_minute": 0, "max_concurrency": 0, "max_request_bytes": 0},
		"trust":         map[string]any{"session_hints": false, "project_hints": false, "intent_hints": false},
		"external_refs": map[string]string{"spaceman.fleet": "tf-test"}, "enabled": true,
	}}, &o, http.StatusCreated); err != nil {
		return fmt.Errorf("binding: %w", err)
	}
	g.BindingID = o.ID
	if g.ServiceToken, err = a.serviceToken(ctx, "spaceman-tf-reader", []string{"usage.read", "config.read"}); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		var par struct {
			Token string `json:"token"`
		}
		if err := a.do(ctx, http.MethodPost, "/api/v1/client-bindings/"+g.BindingID+"/credentials",
			map[string]any{"name": fmt.Sprintf("spaceman-parent-%d", i), "scopes": []string{"gateway:invoke", "gateway:models", "tokens:mint"}},
			&par, http.StatusCreated); err != nil {
			return fmt.Errorf("parent credential: %w", err)
		}
		if !strings.HasPrefix(par.Token, "hpk_") {
			return fmt.Errorf("unexpected parent token format")
		}
		g.ParentTokens = append(g.ParentTokens, par.Token)
	}
	return nil
}
