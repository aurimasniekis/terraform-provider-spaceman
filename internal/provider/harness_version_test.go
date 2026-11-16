package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

func TestHarnessVersionSyntax(t *testing.T) {
	for _, ok := range []string{"", "latest", "stable", "0.0.0", "2.1.3", "10.20.300", "999999.0.1"} {
		if !reHarnessVersion.MatchString(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"Latest", "beta", "next", "2.1", "2.1.3.4", "v2.1.3", "02.1.3", "2.01.3", "2.1.3-beta",
		"2.1.3+b", " 2.1.3", "2.1.3\n", "1234567.0.0", "x.y.z"} {
		if reHarnessVersion.MatchString(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCheckHarnessVersionHarness(t *testing.T) {
	str, null, unknown := types.StringValue, types.StringNull(), types.StringUnknown()
	for _, tc := range []struct {
		name             string
		harness, version types.String
		wantErr          bool
	}{
		{"claude-code pin", str("claude-code"), str("2.1.3"), false},
		{"codex pin", str("codex"), str("latest"), true},
		{"codex clear", str("codex"), str(""), false},
		{"codex unset", str("codex"), null, false},
		{"codex unknown pin", str("codex"), unknown, false},
		{"unknown harness", unknown, str("stable"), false},
	} {
		var d diag.Diagnostics
		checkHarnessVersionHarness(tc.harness, tc.version, &d)
		if d.HasError() != tc.wantErr {
			t.Errorf("%s: diagnostics %v", tc.name, d)
		}
	}
}

// configuredHarnessVersion leaves the field out unless the configuration sets it, and the client then omits it from the
// request body (keep the pin); a configured "" is sent and clears the pin.
func TestConfiguredHarnessVersion(t *testing.T) {
	r := &harnessProfileResource{}
	var sresp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	typ := sresp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	config := func(v tftypes.Value) tfsdk.Config {
		vals := map[string]tftypes.Value{}
		for k, at := range typ.AttributeTypes {
			vals[k] = tftypes.NewValue(at, nil)
		}
		vals["harness_version"] = v
		return tfsdk.Config{Schema: sresp.Schema, Raw: tftypes.NewValue(typ, vals)}
	}
	for _, tc := range []struct {
		name string
		v    tftypes.Value
		want string
	}{
		{"unset", tftypes.NewValue(tftypes.String, nil), `{"name":""}`},
		{"unknown", tftypes.NewValue(tftypes.String, tftypes.UnknownValue), `{"name":""}`},
		{"clear", tftypes.NewValue(tftypes.String, ""), `{"name":"","harness_version":""}`},
		{"pin", tftypes.NewValue(tftypes.String, "2.1.3"), `{"name":"","harness_version":"2.1.3"}`},
	} {
		var d diag.Diagnostics
		hv := configuredHarnessVersion(context.Background(), config(tc.v), &d)
		if d.HasError() {
			t.Fatalf("%s: %v", tc.name, d)
		}
		b, err := json.Marshal(client.ProfileInput{HarnessVersion: hv})
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("%s: body %s, want %s", tc.name, b, tc.want)
		}
	}
}
