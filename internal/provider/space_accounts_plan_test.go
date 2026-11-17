package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
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

// A 0.1.5 state has no priority (null). Read must fill it from the API, so the next plan is empty
// instead of null -> 100.
func TestSpaceAccountsReadFillsPriorityOfLegacyState(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(&fakeAccountsAPI{items: []map[string]any{{"account_id": "acc_1", "default": true, "priority": 100}}})
	defer srv.Close()
	c, err := client.New(srv.URL, "spaceman_at_test", client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := &spaceAccountsResource{base{c: c}}
	var sr fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &sr)
	legacy := types.SetValueMust(bindingType, []attr.Value{types.ObjectValueMust(bindingType.AttrTypes, map[string]attr.Value{
		"account_id": types.StringValue("acc_1"), "default": types.BoolValue(true), "priority": types.Int64Null()})})
	st := tfsdk.State{Schema: sr.Schema, Raw: tftypes.NewValue(sr.Schema.Type().TerraformType(ctx), nil)}
	if d := st.Set(ctx, &spaceAccountsModel{ID: types.StringValue("spc_1"), SpaceID: types.StringValue("spc_1"), Bindings: legacy}); d.HasError() {
		t.Fatal(d)
	}
	resp := fwresource.ReadResponse{State: st}
	r.Read(ctx, fwresource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got spaceAccountsModel
	resp.State.Get(ctx, &got)
	want := types.SetValueMust(bindingType, []attr.Value{types.ObjectValueMust(bindingType.AttrTypes, map[string]attr.Value{
		"account_id": types.StringValue("acc_1"), "default": types.BoolValue(true), "priority": types.Int64Value(100)})})
	if !got.Bindings.Equal(want) {
		t.Fatalf("read = %v, want %v", got.Bindings, want)
	}
}

// Removing an attribute from config must revert it to its default (Optional + Computed would otherwise keep the
// prior state value): this is what the plan modifier adds.
func TestSpaceAccountsRemovedAttributeRevertsToDefault(t *testing.T) {
	cases := map[string]struct{ from, to, attr, before, after string }{
		"default":  {`[{ account_id = "acc_1", default = true }]`, `[{ account_id = "acc_1" }]`, "binding.0.default", "true", "false"},
		"priority": {`[{ account_id = "acc_1", priority = 20 }]`, `[{ account_id = "acc_1" }]`, "binding.0.priority", "20", "100"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(&fakeAccountsAPI{})
			defer srv.Close()
			const res = "spaceman_space_accounts.x"
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: accountsHCL(srv.URL, c.from), Check: resource.TestCheckResourceAttr(res, c.attr, c.before)},
					{
						Config:           accountsHCL(srv.URL, c.to),
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
						Check:            resource.TestCheckResourceAttr(res, c.attr, c.after),
					},
					{Config: accountsHCL(srv.URL, c.to), PlanOnly: true},
				},
			})
		})
	}
}
