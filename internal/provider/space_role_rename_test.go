package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	busy     string // non-empty: rename answers 409 precondition_failed (role_busy) with this detail
	taken    bool   // rename answers 409 already_exists (the name is taken)

	clock       int                         // each write moves the fake clock one minute
	pools       map[string][]map[string]any // role -> pool entries
	grants      map[string]map[string]any   // id -> grant
	nextID      int
	poolPuts    []string // "role" for a write, "role:delete" for an empty one
	grantOps    []string // "add:role", "revoke:id"
	revokeFails bool     // revoking a grant answers 500
}

func (f *fakeRolesAPI) now() string {
	f.clock++
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(f.clock) * time.Minute).Format(time.RFC3339Nano)
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
	r["updated_at"] = f.now()
	f.roles[to] = r
	// Like central, the rename moves what references the role in the same transaction.
	if p, ok := f.pools[from]; ok {
		delete(f.pools, from)
		f.pools[to] = p
	}
	for _, g := range f.grants {
		if g["role"] == from {
			g["role"] = to
		}
	}
	rn := map[string]any{"id": "rrn_" + to, "space_id": "spc_1", "from": from, "to": to, "node_state": "done", "renamed_at": f.now()}
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
		role["updated_at"] = f.now()
		f.roles[name] = role
		_ = json.NewEncoder(w).Encode(role)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/rename"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, "/spaces/spc_1/roles/"), "/rename")
		if f.noRename || f.roles[name] == nil {
			f.problem(w, 404, "", "")
			return
		}
		if f.busy != "" {
			f.problem(w, 409, "precondition_failed", f.busy)
			return
		}
		if f.taken {
			f.problem(w, 409, "already_exists", "a role named that exists")
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
	case r.Method == http.MethodGet && p == "/spaces/spc_1/account-pools":
		out := []map[string]any{}
		for role, es := range f.pools {
			out = append(out, map[string]any{"role": role, "items": es})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "accounts_version": 1})
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/spaces/spc_1/account-pools/"):
		role := strings.TrimPrefix(p, "/spaces/spc_1/account-pools/")
		var in struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(in.Items) == 0 {
			f.poolPuts = append(f.poolPuts, role+":delete")
			delete(f.pools, role)
		} else {
			f.poolPuts = append(f.poolPuts, role)
			f.pools[role] = in.Items
		}
		out := []map[string]any{}
		for r, es := range f.pools {
			out = append(out, map[string]any{"role": r, "items": es})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "accounts_version": 1})
	case r.Method == http.MethodGet && p == "/mcp-servers/mcs_1/grants":
		out := []map[string]any{}
		for _, g := range f.grants {
			out = append(out, g)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
	case r.Method == http.MethodPost && p == "/mcp-servers/mcs_1/grants":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, ex := range f.grants { // UNIQUE (server_id, space_id, role)
			if ex["space_id"] == in["space_id"] && ex["role"] == in["role"] {
				f.problem(w, http.StatusConflict, "already_exists", "grant exists")
				return
			}
		}
		f.nextID++
		id := "mcg_" + strconv.Itoa(f.nextID)
		g := map[string]any{"id": id, "server_id": "mcs_1", "space_id": in["space_id"], "role": in["role"], "tools": []string{}}
		f.grants[id] = g
		f.grantOps = append(f.grantOps, "add:"+in["role"].(string))
		_ = json.NewEncoder(w).Encode(g)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/mcp-servers/mcs_1/grants/"):
		id := strings.TrimPrefix(p, "/mcp-servers/mcs_1/grants/")
		f.grantOps = append(f.grantOps, "revoke:"+id)
		if f.revokeFails {
			f.problem(w, http.StatusInternalServerError, "internal", "revoke failed")
			return
		}
		delete(f.grants, id)
		w.WriteHeader(http.StatusNoContent)
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
	f := &fakeRolesAPI{roles: map[string]map[string]any{}, pools: map[string][]map[string]any{}, grants: map[string]map[string]any{}}
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
		"busy": {func(f *fakeRolesAPI) {
			f.busy = "the role implementor is busy (role_busy); stop or finish first: run run_9"
		}, `(?s)run_9.*cannot\s+be\s+renamed`},
		"taken":  {func(f *fakeRolesAPI) { f.taken = true }, `(?s)already\s+used`},
		"no-api": {func(f *fakeRolesAPI) { f.noRename = true }, "needs SpaceMan 0.1.11 or later"},
	} {
		t.Run(name, func(t *testing.T) {
			f, url := newFakeRoles(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: roleRenameHCL(url, "implementor", "a")},
					{PreConfig: func() { c.set(f) }, Config: roleRenameHCL(url, "implementor-hard", "a"), ExpectError: regexp.MustCompile(c.want)},
					{PreConfig: func() { f.busy, f.noRename, f.taken = "", false, false }, Config: roleRenameHCL(url, "implementor", "a"), PlanOnly: true},
				},
			})
			if f.creates != 1 {
				t.Fatalf("creates=%d, want 1", f.creates)
			}
		})
	}
}

// roleDependentsHCL is a role with a pool and a grant that reference it. role is the role's name; poolRole and grantRole
// are what the pool and the grant name (the role's name, or a literal for a retarget).
func roleDependentsHCL(url, role, poolRole, grantRole string) string {
	ref := func(v string) string {
		if v == "" {
			return "spaceman_space_role.x.name"
		}
		return `"` + v + `"`
	}
	return roleRenameHCL(url, role, "a") + `
resource "spaceman_space_role_pool" "p" {
  space_id = "spc_1"
  role     = ` + ref(poolRole) + `
  account  = [{ account_id = "acc_1" }]
}
resource "spaceman_mcp_grant" "g" {
  server_id = "mcs_1"
  space_id  = "spc_1"
  role      = ` + ref(grantRole) + `
}`
}

const (
	poolRes  = "spaceman_space_role_pool.p"
	grantRes = "spaceman_mcp_grant.g"
)

func (f *fakeRolesAPI) snapshot() (puts, ops []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.poolPuts...), append([]string{}, f.grantOps...)
}

// Renaming the role updates the pool and the grant in place: neither is destroyed or re-created, the grant keeps its id,
// and the pool is only written again under its new name (central already moved it).
func TestSpaceRoleRenameKeepsPoolAndGrant(t *testing.T) {
	f, url := newFakeRoles(t)
	update := func(addrs ...string) resource.ConfigPlanChecks {
		var cs []plancheck.PlanCheck
		for _, a := range addrs {
			cs = append(cs, plancheck.ExpectResourceAction(a, plancheck.ResourceActionUpdate))
		}
		cs = append(cs, plancheck.ExpectUnknownValue(poolRes, tfjsonpath.New("id")), plancheck.ExpectUnknownValue(grantRes, tfjsonpath.New("id")))
		return resource.ConfigPlanChecks{PreApply: cs}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleDependentsHCL(url, "implementor", "", ""), Check: resource.TestCheckResourceAttr(grantRes, "id", "mcg_1")},
			{Config: roleDependentsHCL(url, "implementor-hard", "", ""), ConfigPlanChecks: update(roleRes, poolRes, grantRes),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(grantRes, "id", "mcg_1"), // the same grant
					resource.TestCheckResourceAttr(grantRes, "role", "implementor-hard"),
					resource.TestCheckResourceAttr(poolRes, "id", "spc_1/implementor-hard"),
					resource.TestCheckResourceAttr(poolRes, "role", "implementor-hard"))},
			{Config: roleDependentsHCL(url, "implementor-hard", "", ""), PlanOnly: true},
		},
	})
	puts, ops := f.snapshot()
	if got := strings.Join(ops, ","); got != "add:implementor,revoke:mcg_1" { // the final destroy is the only revoke
		t.Fatalf("grant calls = %s, want one add and the destroy's revoke", got)
	}
	// Create, the rename's write under the new name, then the destroy's delete: never a delete of the old name.
	if got := strings.Join(puts, ","); got != "implementor,implementor-hard,implementor-hard:delete" {
		t.Fatalf("pool writes = %s", got)
	}
}

// A refused rename leaves the pool and the grant exactly as they were: they are not touched before the rename succeeds.
func TestSpaceRoleRenameRefusedLeavesPoolAndGrant(t *testing.T) {
	f, url := newFakeRoles(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleDependentsHCL(url, "implementor", "", "")},
			{PreConfig: func() { f.busy = "the role implementor is busy (role_busy); stop or finish first: run run_9" }, Config: roleDependentsHCL(url, "implementor-hard", "", ""), ExpectError: regexp.MustCompile("run_9")},
			{PreConfig: func() {
				puts, ops := f.snapshot()
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(puts) != 1 || len(ops) != 1 || len(f.pools["implementor"]) != 1 || f.grants["mcg_1"]["role"] != "implementor" {
					t.Errorf("pool writes %v, grant calls %v, pools %v, grants %v: the refused rename touched a dependent", puts, ops, f.pools, f.grants)
				}
				f.busy = ""
			}, Config: roleDependentsHCL(url, "implementor", "", ""), PlanOnly: true},
		},
	})
}

// A pool or grant pointed at a different role (not a rename) is a real retarget: the pool is written under the new name
// and the old name's pool removed; the grant is added for the new role and then the old one revoked. Both are in-place updates, never replacements.
func TestSpaceRolePoolAndGrantRetarget(t *testing.T) {
	f, url := newFakeRoles(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleDependentsHCL(url, "implementor", "", "")},
			{Config: roleDependentsHCL(url, "implementor", "other", "other"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(poolRes, plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction(grantRes, plancheck.ResourceActionUpdate)}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(poolRes, "id", "spc_1/other"),
					resource.TestCheckResourceAttr(grantRes, "id", "mcg_2"),
					resource.TestCheckResourceAttr(grantRes, "role", "other"))},
			{Config: roleDependentsHCL(url, "implementor", "other", "other"), PlanOnly: true},
			{PreConfig: func() {
				puts, ops := f.snapshot()
				if got := strings.Join(puts, ","); got != "implementor,other,implementor:delete" {
					t.Errorf("pool writes = %s", got)
				}
				if got := strings.Join(ops, ","); got != "add:implementor,add:other,revoke:mcg_1" {
					t.Errorf("grant calls = %s", got)
				}
			}, Config: roleDependentsHCL(url, "implementor", "other", "other"), PlanOnly: true},
		},
	})
}

// When the old grant cannot be revoked after the new one was added, the new grant is still saved in state (an error names
// the old one), so the next apply converges instead of adding the role's grant again and hitting the uniqueness conflict.
func TestSpaceRoleGrantRetargetRevokeFailureConverges(t *testing.T) {
	f, url := newFakeRoles(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleDependentsHCL(url, "implementor", "", "")},
			{PreConfig: func() { f.revokeFails = true },
				Config:      roleDependentsHCL(url, "implementor", "", "other"),
				ExpectError: regexp.MustCompile(`(?s)old\s+MCP\s+grant.*mcg_1`)},
			{PreConfig: func() {
				if g := f.grants["mcg_2"]; g == nil || g["role"] != "other" {
					t.Errorf("grants = %v, want the new grant added", f.grants)
				}
				f.revokeFails = false
			}, Config: roleDependentsHCL(url, "implementor", "", "other"),
				Check: resource.TestCheckResourceAttr(grantRes, "id", "mcg_2")},
			{Config: roleDependentsHCL(url, "implementor", "", "other"), PlanOnly: true},
		},
	})
}

// A grant for the new role that already exists (409 already_exists) is adopted instead of failing, and the old one revoked.
func TestSpaceRoleGrantRetargetAdoptsExisting(t *testing.T) {
	f, url := newFakeRoles(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleDependentsHCL(url, "implementor", "", "")},
			{PreConfig: func() {
				f.grants["mcg_9"] = map[string]any{"id": "mcg_9", "server_id": "mcs_1", "space_id": "spc_1", "role": "other", "tools": []string{}}
			}, Config: roleDependentsHCL(url, "implementor", "", "other"),
				Check: resource.TestCheckResourceAttr(grantRes, "id", "mcg_9")},
			{Config: roleDependentsHCL(url, "implementor", "", "other"), PlanOnly: true},
			{PreConfig: func() {
				if _, ok := f.grants["mcg_1"]; ok {
					t.Errorf("old grant not revoked: %v", f.grants)
				}
			}, Config: roleDependentsHCL(url, "implementor", "", "other"), PlanOnly: true},
		},
	})
}

// A name reused by a new role is not the old role: a rename made before the role was last seen is not followed, so the
// resource is removed from state (and planned as a create), never adopted under the name it was once renamed to.
func TestSpaceRoleReadIgnoresRenameOfEarlierHolder(t *testing.T) {
	f, url := newFakeRoles(t)
	f.roles["elsewhere"] = map[string]any{"space_id": "spc_1", "name": "elsewhere", "revision": 1, "version": 1, "template_id": "rtp_1",
		"settings": map[string]string{}, "skills": []any{}, "updated_at": f.now()}
	// An old holder of "implementor" was renamed to "elsewhere" long before this role existed.
	f.renames = []map[string]any{{"id": "rrn_old", "space_id": "spc_1", "from": "implementor", "to": "elsewhere", "node_state": "done",
		"renamed_at": "2025-01-01T00:00:00Z"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleRenameHCL(url, "implementor", "a")},
			{PreConfig: func() { f.mu.Lock(); delete(f.roles, "implementor"); f.mu.Unlock() }, Config: roleRenameHCL(url, "implementor", "a"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(roleRes, plancheck.ResourceActionCreate)}}},
		},
	})
	if f.roles["elsewhere"] == nil || f.creates != 2 {
		t.Fatalf("elsewhere = %v, creates = %d: the unrelated role was adopted or touched", f.roles["elsewhere"], f.creates)
	}
}

// Importing a role gives the same guard a baseline: a name that was renamed away before the import is not followed.
func TestSpaceRoleImportRecordsBaseline(t *testing.T) {
	f, url := newFakeRoles(t)
	f.roles["elsewhere"] = map[string]any{"space_id": "spc_1", "name": "elsewhere", "revision": 1, "version": 1, "template_id": "rtp_1",
		"settings": map[string]string{}, "skills": []any{}, "updated_at": f.now()}
	f.roles["implementor"] = map[string]any{"space_id": "spc_1", "name": "implementor", "revision": 1, "version": 1, "template_id": "rtp_1",
		"settings": map[string]string{}, "skills": []any{}, "updated_at": f.now()}
	f.renames = []map[string]any{{"id": "rrn_old", "space_id": "spc_1", "from": "implementor", "to": "elsewhere", "node_state": "done",
		"renamed_at": "2020-01-01T00:00:00Z"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: roleRenameHCL(url, "implementor", ""), ResourceName: roleRes, ImportState: true, ImportStateId: "spc_1/implementor", ImportStatePersist: true},
			{PreConfig: func() { f.mu.Lock(); delete(f.roles, "implementor"); f.mu.Unlock() }, Config: roleRenameHCL(url, "implementor", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(roleRes, plancheck.ResourceActionCreate)}}},
		},
	})
	if f.roles["elsewhere"] == nil {
		t.Fatal("the unrelated role was touched")
	}
}
