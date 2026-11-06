package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestClaudeSettingsValue(t *testing.T) {
	null := jsontypes.NewNormalizedNull()
	empty := jsontypes.NewNormalizedValue("{}")
	spaced := jsontypes.NewNormalizedValue("{ }")
	set := jsontypes.NewNormalizedValue(`{"model":"opus"}`)
	for _, tc := range []struct {
		name   string
		server string
		held   jsontypes.Normalized
		want   jsontypes.Normalized
	}{
		{"none held null", "", null, null},
		{"server {} held null", "{}", null, null},
		{"none held {}", "", empty, empty},
		{"server {} held {}", "{}", empty, empty},
		{"held {} as written", "", spaced, spaced},
		{"cleared elsewhere is drift", "", set, null},
		{"unknown held", "", jsontypes.NewNormalizedUnknown(), null},
		{"server settings", `{"model":"opus"}`, empty, set},
	} {
		if got := claudeSettingsValue(json.RawMessage(tc.server), tc.held); !got.Equal(tc.want) {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func validateClaudeSettings(v string) diag.Diagnostics {
	resp := &validator.StringResponse{}
	jsonObjectValidator{}.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("claude_settings"), ConfigValue: types.StringValue(v)}, resp)
	return resp.Diagnostics
}

func TestClaudeSettingsEnvWarning(t *testing.T) {
	d := validateClaudeSettings(`{"env":{"FOO":"bar","ANTHROPIC_BASE_URL":"x","https_proxy":"y","CLAUDE_CODE_USE_BEDROCK":"1"}}`)
	if d.HasError() || d.WarningsCount() != 1 {
		t.Fatalf("want one warning: %v", d)
	}
	detail := d.Warnings()[0].Detail()
	if !strings.Contains(detail, "ANTHROPIC_BASE_URL, CLAUDE_CODE_USE_BEDROCK, https_proxy") || strings.Contains(detail, "FOO") {
		t.Fatalf("warning detail: %s", detail)
	}
	for _, ok := range []string{`{}`, `{"env":{"FOO":"bar","NO_PROXY_LIST":"x"}}`, `{"env":"not an object"}`} {
		if d := validateClaudeSettings(ok); len(d) != 0 {
			t.Errorf("%s: unexpected diagnostics %v", ok, d)
		}
	}
	if d := validateClaudeSettings(`[1]`); !d.HasError() {
		t.Error("[1] must be an error")
	}
}
