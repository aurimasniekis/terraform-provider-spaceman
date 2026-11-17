package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// fakeAccountsAPI is a SpaceMan stand-in for /spaces/{id}/accounts: PUT stores the bindings, GET returns them,
// filling priority with 100 like central does.
type fakeAccountsAPI struct {
	mu    sync.Mutex
	items []map[string]any
}

func (f *fakeAccountsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPut {
		var in struct {
			Items []map[string]any `json:"items"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &in)
		f.items = nil
		for _, it := range in.Items {
			if _, ok := it["priority"]; !ok {
				it["priority"] = 100
			}
			f.items = append(f.items, it)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	items := f.items
	if items == nil {
		items = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func accountsHCL(url, bindings string) string {
	return `
provider "spaceman" {
  endpoint = "` + url + `"
  token    = "spaceman_at_test"
}
resource "spaceman_space_accounts" "x" {
  space_id = "spc_1"
  binding = ` + bindings + `
}`
}

func TestSpaceAccountsDefaultSurvivesPlan(t *testing.T) {
	cases := map[string]string{
		"literal":  `[{ account_id = "acc_1", default = true }]`,
		"index0":   `[for i, a in ["acc_1"] : { account_id = a, default = (i == 0) }]`,
		"multiple": `[{ account_id = "acc_1", default = true }, { account_id = "acc_2" }]`,
		"explicit": `[{ account_id = "acc_1", default = true, priority = 20 }]`,
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(&fakeAccountsAPI{})
			defer srv.Close()
			cfg := accountsHCL(srv.URL, b)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: cfg, Check: resource.TestCheckResourceAttr("spaceman_space_accounts.x", "binding.#", map[bool]string{true: "2", false: "1"}[name == "multiple"])},
					{Config: cfg, PlanOnly: true},
					{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
				},
			})
		})
	}
}
