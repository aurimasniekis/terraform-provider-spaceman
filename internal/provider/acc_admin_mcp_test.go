package provider

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP (RFC 6238) is defined over HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// totpCode is the RFC 6238 code (SHA-1, 6 digits, 30 s) of a base32 secret at t.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		t.Fatalf("TOTP secret: %v", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30)) //nolint:gosec // the time is positive
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// accAPI calls the dev server's API directly: with bearer (when set), the session cookies of hc and csrf.
func accAPI(t *testing.T, hc *http.Client, bearer, csrf, method, path string, in, out any) int {
	t.Helper()
	var body *bytes.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, acc.endpoint+"/api/v1"+path, body) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else {
		req.Header.Set("Origin", acc.endpoint) // a browser-like same-origin request
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if method == http.MethodPost {
		req.Header.Set("Idempotency-Key", "tf-acc-"+randHex(8))
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(resp.Body)
	if resp.StatusCode >= 300 {
		// Problem bodies carry no secrets (the token secret is only in a 201 body).
		t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw.String())
	}
	if out != nil && raw.Len() > 0 {
		if err := json.Unmarshal(raw.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// adminMCPUserToken creates a global administrator with a password and TOTP, signs in, and issues a never-expiring
// API token for them: a SpaceMan MCP server's internal token must be owned by a user, so the root token cannot create
// one (SpaceMan ADR-0063). The secret goes only into the provider's environment for this test; it is never printed.
func adminMCPUserToken(t *testing.T) string {
	t.Helper()
	email, password := "tf-mcp-"+suffix()+"@example.com", "pw-"+randHex(16)
	accAPI(t, http.DefaultClient, acc.token, "", http.MethodPost, "/users", map[string]any{
		"email": email, "display_name": "TF admin MCP", "password": password,
		"grants": []map[string]string{{"role": "admin", "domain": "*"}}}, nil)
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	accAPI(t, hc, "", "", http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password}, &me)
	var enroll struct {
		Secret string `json:"secret"`
	}
	accAPI(t, hc, "", me.CSRF, http.MethodPost, "/auth/mfa/enroll", nil, &enroll)
	var confirmed struct {
		Me struct {
			CSRF string `json:"csrf_token"`
		} `json:"me"`
	}
	accAPI(t, hc, "", me.CSRF, http.MethodPost, "/auth/mfa/enroll/confirm", map[string]string{"code": totpCode(t, enroll.Secret, time.Now())}, &confirmed)
	csrf := me.CSRF
	if confirmed.Me.CSRF != "" {
		csrf = confirmed.Me.CSRF
	}
	// Token creation needs a fresh second factor. The enrolment code is spent, so use the next time step's code (within
	// SpaceMan's one-step skew).
	var verified struct {
		CSRF string `json:"csrf_token"`
	}
	accAPI(t, hc, "", csrf, http.MethodPost, "/auth/mfa/verify", map[string]string{"code": totpCode(t, enroll.Secret, time.Now().Add(30*time.Second))}, &verified)
	if verified.CSRF != "" {
		csrf = verified.CSRF
	}
	var created struct {
		Secret string `json:"secret"`
	}
	accAPI(t, hc, "", csrf, http.MethodPost, "/tokens", map[string]any{"name": "terraform", "never_expires": true,
		"grants": []map[string]string{{"role": "admin", "domain": "*"}}}, &created)
	if created.Secret == "" {
		t.Fatal("create-api-token returned no secret")
	}
	return created.Secret
}

// tokenRevoked reports whether SpaceMan lists the API token id as revoked (list-all-tokens, with the root token).
func tokenRevoked(t *testing.T, id string) (bool, error) {
	t.Helper()
	var out struct {
		Items []struct {
			ID        string  `json:"id"`
			RevokedAt *string `json:"revoked_at"`
		} `json:"items"`
	}
	accAPI(t, http.DefaultClient, acc.token, "", http.MethodGet, "/admin/tokens", nil, &out)
	for _, tok := range out.Items {
		if tok.ID == id {
			return tok.RevokedAt != nil && *tok.RevokedAt != "", nil
		}
	}
	return false, fmt.Errorf("token %s is not listed", id)
}

func checkTokenRevoked(t *testing.T, id *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if *id == "" {
			return fmt.Errorf("no token ID was recorded")
		}
		revoked, err := tokenRevoked(t, *id)
		if err != nil {
			return err
		}
		if !revoked {
			return fmt.Errorf("token %s is still active", *id)
		}
		return nil
	}
}

func adminMCPHCL(sfx, spaceSlug, nodeID, slug, preset, role, extra string) string {
	return fmt.Sprintf(`
resource "spaceman_space" "s" {
  slug             = %[2]q
  name             = "Admin MCP"
  node_id          = %[3]q
  purge_on_destroy = true
}

resource "spaceman_mcp_server" "m" {
  slug          = "tfh%[1]s"
  name          = "Tracker"
  url           = "http://127.0.0.1:9/mcp"
  allow_private = true
}

resource "spaceman_admin_mcp_server" "a" {
  slug   = %[4]q
  name   = "SpaceMan admin"
  preset = %[5]q
  grants = [{ role = %[6]q, domain = spaceman_space.s.id }]
  %[7]s
}

resource "spaceman_mcp_grant" "g" {
  server_id = spaceman_admin_mcp_server.a.id
  space_id  = spaceman_space.s.id
}
`, sfx, spaceSlug, nodeID, slug, preset, role, extra)
}

// effectiveTools checks the server's effective tools: every name in has is offered and none in hasNot.
func effectiveTools(has, hasNot []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		r := s.RootModule().Resources["spaceman_admin_mcp_server.a"]
		if r == nil {
			return fmt.Errorf("no spaceman_admin_mcp_server.a in the state")
		}
		var tools []string
		for k, v := range r.Primary.Attributes {
			if strings.HasPrefix(k, "effective_tools.") && k != "effective_tools.#" {
				tools = append(tools, v)
			}
		}
		if len(tools) == 0 {
			return fmt.Errorf("effective_tools is empty")
		}
		for _, n := range has {
			if !slices.Contains(tools, n) {
				return fmt.Errorf("effective_tools lacks %s (%d tools)", n, len(tools))
			}
		}
		for _, n := range hasNot {
			if slices.Contains(tools, n) {
				return fmt.Errorf("effective_tools offers %s", n)
			}
		}
		return nil
	}
}

// knownString is a known-value check against a string recorded by an earlier step (read when the check runs).
type knownStringCheck struct{ want *string }

func knownString(want *string) knownvalue.Check { return knownStringCheck{want} }

func (c knownStringCheck) CheckValue(v any) error {
	if got, ok := v.(string); !ok || got != *c.want {
		return fmt.Errorf("value %v, want %q", v, *c.want)
	}
	return nil
}

func (c knownStringCheck) String() string { return *c.want }

func recordAttr(res, key string, into *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*into = stateAttr(s, res, key)
		if *into == "" {
			return fmt.Errorf("%s.%s is empty", res, key)
		}
		return nil
	}
}

func sameAttr(res, key string, want *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if got := stateAttr(s, res, key); got != *want {
			return fmt.Errorf("%s.%s = %q, want %q", res, key, got, *want)
		}
		return nil
	}
}

// TestAccAdminMCP covers spaceman_admin_mcp_server (SpaceMan ADR-0063): create with effective_tools, an in-place preset
// change (same token), a grants change (re-issued token, the old one revoked), import, the cross-kind guards, plan-time
// refusals, and destroy revoking the token.
func TestAccAdminMCP(t *testing.T) {
	testAccPreCheck(t)
	t.Setenv(EnvToken, adminMCPUserToken(t)) // the provider under test acts as that user (read at configure time)
	sfx, spaceSlug := suffix(), "tf-amcp-"+suffix()
	slug := "tfa" + sfx
	var tok1, tok2 string
	cfg := func(preset, role, extra string) string {
		return adminMCPHCL(sfx, spaceSlug, acc.nodeA.ID, slug, preset, role, extra)
	}
	accCase(t, checkTokenRevoked(t, &tok2),
		resource.TestStep{
			Config: cfg("read", "viewer", ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "preset", "read"),
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "approval", "none"),
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "enabled", "true"),
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "never_expires", "true"),
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "expires_at", ""),
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "token_state", "active"),
				resource.TestCheckResourceAttrSet("spaceman_admin_mcp_server.a", "owner_user_id"),
				resource.TestCheckResourceAttrSet("spaceman_admin_mcp_server.a", "version"),
				effectiveTools([]string{"list_tasks", "get_task"}, []string{"create_task", "delete_schedule"}),
				recordAttr("spaceman_admin_mcp_server.a", "token_id", &tok1),
			),
		},
		// Plan-time refusals: before a valid apply, so the post-test destroy uses a valid configuration.
		resource.TestStep{Config: adminMCPHCL(sfx, spaceSlug, acc.nodeA.ID, "spaceman", "read", "viewer", ""),
			ExpectError: regexp.MustCompile(`(?s)slug.*spaceman`)},
		resource.TestStep{Config: strings.Replace(cfg("read", "viewer", ""), `grants = [{ role = "viewer", domain = spaceman_space.s.id }]`, "", 1),
			ExpectError: regexp.MustCompile(`The read preset needs grants`)},
		resource.TestStep{Config: cfg("read", "viewer", "never_expires = true\n  expires_in_days = 30"),
			ExpectError: regexp.MustCompile(`Conflicting expiry`)},
		// A preset change is an in-place edit: the same token.
		resource.TestStep{
			Config: cfg("operate", "viewer", ""),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_admin_mcp_server.a", plancheck.ResourceActionUpdate),
				plancheck.ExpectKnownValue("spaceman_admin_mcp_server.a", tfjsonpath.New("token_id"), knownString(&tok1)),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "preset", "operate"),
				sameAttr("spaceman_admin_mcp_server.a", "token_id", &tok1),
			),
		},
		// A grants change re-issues the token in place: a new token_id, the old token revoked.
		resource.TestStep{
			Config: cfg("operate", "operator", ""),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("spaceman_admin_mcp_server.a", plancheck.ResourceActionUpdate),
				plancheck.ExpectUnknownValue("spaceman_admin_mcp_server.a", tfjsonpath.New("token_id")),
			}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "grants.0.role", "operator"),
				recordAttr("spaceman_admin_mcp_server.a", "token_id", &tok2),
				func(*terraform.State) error {
					if tok2 == tok1 {
						return fmt.Errorf("token_id did not change (%s)", tok1)
					}
					return nil
				},
				checkTokenRevoked(t, &tok1),
				effectiveTools([]string{"list_tasks", "create_task"}, []string{"delete_schedule"}),
			),
		},
		// An expiry re-issues too; approval and named tools are in-place edits.
		resource.TestStep{
			Config: cfg("operate", "operator", "expires_in_days = 30\n  approval = \"writes\"\n  tools = [\"delete_schedule\"]"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "never_expires", "false"),
				resource.TestCheckResourceAttrSet("spaceman_admin_mcp_server.a", "expires_at"),
				resource.TestCheckResourceAttr("spaceman_admin_mcp_server.a", "approval", "writes"),
				effectiveTools([]string{"create_task", "delete_schedule"}, nil),
				checkTokenRevoked(t, &tok2),
				recordAttr("spaceman_admin_mcp_server.a", "token_id", &tok2),
			),
		},
		resource.TestStep{ResourceName: "spaceman_admin_mcp_server.a", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"expires_in_days"}},
		// Back to a token that never expires, then import with nothing ignored.
		resource.TestStep{Config: cfg("operate", "operator", ""),
			Check: recordAttr("spaceman_admin_mcp_server.a", "token_id", &tok2)},
		resource.TestStep{ResourceName: "spaceman_admin_mcp_server.a", ImportState: true, ImportStateVerify: true},
		// Cross-kind guards: each resource refuses the other kind.
		resource.TestStep{ResourceName: "spaceman_admin_mcp_server.a", ImportState: true,
			ImportStateIdFunc: func(s *terraform.State) (string, error) { return stateAttr(s, "spaceman_mcp_server.m", "id"), nil },
			ExpectError:       regexp.MustCompile(`manage it with spaceman_mcp_server`)},
		resource.TestStep{ResourceName: "spaceman_mcp_server.m", ImportState: true,
			ImportStateIdFunc: func(s *terraform.State) (string, error) {
				return stateAttr(s, "spaceman_admin_mcp_server.a", "id"), nil
			},
			ExpectError: regexp.MustCompile(`manage it with spaceman_admin_mcp_server`)},
	)
}
