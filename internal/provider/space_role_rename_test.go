package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// fakeRolesAPI is a SpaceMan stand-in for a space's roles: save upserts, rename moves the role and records it.
type fakeRolesAPI struct {
	mu       sync.Mutex
	roles    map[string]map[string]any
	renames  []map[string]any
	creates  int
	deletes  int
	noRename bool   // an older SpaceMan: the rename path does not exist
	busy     string // non-empty: rename answers 409 role_busy with this detail
}

func (f *fakeRolesAPI) problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "detail": detail})
}

// renameOutside renames a role the way another client would.
func (f *fakeRolesAPI) renameOutside(from, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.move(from, to)
}

func (f *fakeRolesAPI) move(from, to string) map[string]any {
	r := f.roles[from]
	delete(f.roles, from)
	r["name"] = to
	r["revision"] = r["revision"].(int) + 1
	f.roles[to] = r
	rn := map[string]any{"id": "rrn_" + to, "space_id": "spc_1", "from": from, "to": to, "node_state": "done"}
	f.renames = append([]map[string]any{rn}, f.renames...)
	return rn
}

func (f *fakeRolesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	p := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case r.Method == http.MethodGet && p == "/spaces/spc_1/roles":
		out := []map[string]any{}
		for _, v := range f.roles {
			out = append(out, v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
	case r.Method == http.MethodGet && p == "/spaces/spc_1/role-renames":
		if f.noRename {
			f.problem(w, 404, "", "")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": append([]map[string]any{}, f.renames...)})
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/spaces/spc_1/roles/"):
		name := strings.TrimPrefix(p, "/spaces/spc_1/roles/")
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		role, ok := f.roles[name]
		if !ok {
			f.creates++
			role = map[string]any{"space_id": "spc_1", "name": name, "revision": 0, "version": 1, "settings": map[string]string{}, "skills": []any{}}
		}
		role["template_id"] = in["template_id"]
		role["instructions"] = in["instructions"]
		role["revision"] = role["revision"].(int) + 1
		f.roles[name] = role
		_ = json.NewEncoder(w).Encode(role)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/rename"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, "/spaces/spc_1/roles/"), "/rename")
		if f.noRename || f.roles[name] == nil {
			f.problem(w, 404, "", "")
			return
		}
		if f.busy != "" {
			f.problem(w, 409, "role_busy", f.busy)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		rn := f.move(name, in["new_name"])
		out := map[string]any{"rename": rn}
		for k, v := range f.roles[in["new_name"]] {
			out[k] = v
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/spaces/spc_1/roles/"):
		f.deletes++
		delete(f.roles, strings.TrimPrefix(p, "/spaces/spc_1/roles/"))
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func roleRenameHCL(url, name, instructions string) string {
	return `
provider "spaceman" {
  endpoint = "` + url + `"
  token    = "spaceman_at_test"
}
resource "spaceman_space_role" "x" {
  space_id     = "spc_1"
  name         = "` + name + `"
  template_id  = "rtp_1"
  instructions = "` + instructions + `"
}`
}

func newFakeRoles(t *testing.T) (*fakeRolesAPI, string) {
	f := &fakeRolesAPI{roles: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

const roleRes = "spaceman_space_role.x"

// Changing `name` plans an in-place update (never a replace) with the id unknown, renames through the API and never
// deletes or re-creates the role; a rename made outside Terraform shows as an in-place name change back.
func TestSpaceRoleRenameIsInPlace(t *testing.T) {
	f, url := newFakeRoles(t)
	update := []plancheck.PlanCheck{plancheck.ExpectResourceAction(roleRes, plancheck.ResourceActionUpdate),
		plancheck.ExpectUnknownValue(roleRes, tfjsonpath.New("id"))}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleRenameHCL(url, "implementor", "a"), Check: resource.TestCheckResourceAttr(roleRes, "id", "spc_1/implementor")},
			{Config: roleRenameHCL(url, "implementor-hard", "a"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: update},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(roleRes, "id", "spc_1/implementor-hard"),
					resource.TestCheckResourceAttr(roleRes, "name", "implementor-hard"))},
			{Config: roleRenameHCL(url, "implementor-hard", "a"), PlanOnly: true},
			// Rename and another attribute in one apply.
			{Config: roleRenameHCL(url, "implementor-x", "b"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: update},
				Check: resource.TestCheckResourceAttr(roleRes, "instructions", "b")},
			// A rename outside Terraform: an in-place name change, applying renames the role back.
			{PreConfig: func() { f.renameOutside("implementor-x", "elsewhere") }, Config: roleRenameHCL(url, "implementor-x", "b"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: update},
				Check:            resource.TestCheckResourceAttr(roleRes, "id", "spc_1/implementor-x")},
			{Config: roleRenameHCL(url, "implementor-x", "b"), PlanOnly: true},
		},
	})
	if f.creates != 1 || f.deletes != 1 { // the final destroy is the only delete
		t.Fatalf("creates=%d deletes=%d, want 1 and 1", f.creates, f.deletes)
	}
}

// A rename refused with role_busy surfaces the blockers; an older SpaceMan (no rename endpoint) gets a clear error.
// Neither falls back to delete and create.
func TestSpaceRoleRenameRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		set  func(f *fakeRolesAPI)
		want string
	}{
		"busy":   {func(f *fakeRolesAPI) { f.busy = "stop run run_9" }, "run_9"},
		"no-api": {func(f *fakeRolesAPI) { f.noRename = true }, "needs SpaceMan 0.1.11 or later"},
	} {
		t.Run(name, func(t *testing.T) {
			f, url := newFakeRoles(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: roleRenameHCL(url, "implementor", "a")},
					{PreConfig: func() { c.set(f) }, Config: roleRenameHCL(url, "implementor-hard", "a"), ExpectError: regexp.MustCompile(c.want)},
					{PreConfig: func() { f.busy, f.noRename = "", false }, Config: roleRenameHCL(url, "implementor", "a"), PlanOnly: true},
				},
			})
			if f.creates != 1 {
				t.Fatalf("creates=%d, want 1", f.creates)
			}
		})
	}
}
