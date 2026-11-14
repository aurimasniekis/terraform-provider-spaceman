package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The Telegram settings schemas are kept in step with SpaceMan's API: the attributes and the bounds of every value are
// compared with the OpenAPI document of the SpaceMan checkout (SPACEMAN_SRC, or ../spaceman). Without a checkout, or
// with one that predates the settings operations, the test is skipped.

type oaProp struct {
	Type             any      `json:"type"`
	Minimum          *float64 `json:"minimum"`
	Maximum          *float64 `json:"maximum"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum"`
	Enum             []string `json:"enum"`
	Pattern          string   `json:"pattern"`
	Ref              string   `json:"$ref"`
}

func loadOpenAPISchemas(t *testing.T) map[string]struct {
	Properties map[string]oaProp `json:"properties"`
} {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Skipf("no SpaceMan checkout: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "api", "openapi.json"))
	if err != nil {
		t.Skipf("no OpenAPI document: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]oaProp `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Components.Schemas["SettingsInput"]; !ok {
		t.Skip("the SpaceMan checkout has no Telegram settings operations")
	}
	return doc.Components.Schemas
}

func validateInt(vs []validator.Int64, v int64) bool {
	for _, x := range vs {
		resp := &validator.Int64Response{}
		x.ValidateInt64(context.Background(), validator.Int64Request{Path: path.Root("x"), ConfigValue: types.Int64Value(v)}, resp)
		if resp.Diagnostics.HasError() {
			return false
		}
	}
	return true
}

func validateFloat(vs []validator.Float64, v float64) bool {
	for _, x := range vs {
		resp := &validator.Float64Response{}
		x.ValidateFloat64(context.Background(), validator.Float64Request{Path: path.Root("x"), ConfigValue: types.Float64Value(v)}, resp)
		if resp.Diagnostics.HasError() {
			return false
		}
	}
	return true
}

func validateString(vs []validator.String, v string) bool {
	for _, x := range vs {
		resp := &validator.StringResponse{}
		x.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("x"), ConfigValue: types.StringValue(v)}, resp)
		if resp.Diagnostics.HasError() {
			return false
		}
	}
	return true
}

// compareAttrs checks attrs against one OpenAPI input object: same names, same bounds and choices.
func compareAttrs(t *testing.T, name string, attrs map[string]schema.Attribute, props map[string]oaProp, skip ...string) {
	t.Helper()
	skipped := map[string]bool{"$schema": true}
	for _, s := range skip {
		skipped[s] = true
	}
	var have, want []string
	for k := range attrs {
		if !skipped[k] {
			have = append(have, k)
		}
	}
	for k := range props {
		if !skipped[k] {
			want = append(want, k)
		}
	}
	sort.Strings(have)
	sort.Strings(want)
	if len(have) != len(want) {
		t.Errorf("%s: attributes %v, API fields %v", name, have, want)
		return
	}
	for i := range have {
		if have[i] != want[i] {
			t.Errorf("%s: attributes %v, API fields %v", name, have, want)
			return
		}
	}
	for k, a := range attrs {
		p, ok := props[k]
		if !ok || skipped[k] {
			continue
		}
		switch a := a.(type) {
		case schema.Int64Attribute:
			if p.Minimum == nil || p.Maximum == nil {
				t.Errorf("%s.%s: the API has no bounds to compare with", name, k)
				continue
			}
			lo, hi := int64(*p.Minimum), int64(*p.Maximum)
			if !validateInt(a.Validators, lo) || !validateInt(a.Validators, hi) || validateInt(a.Validators, lo-1) || validateInt(a.Validators, hi+1) {
				t.Errorf("%s.%s: validators do not match the API bounds %d..%d", name, k, lo, hi)
			}
		case schema.Float64Attribute:
			if p.Maximum == nil || p.ExclusiveMinimum == nil {
				t.Errorf("%s.%s: the API has no bounds to compare with", name, k)
				continue
			}
			if !validateFloat(a.Validators, *p.Maximum) || validateFloat(a.Validators, *p.Maximum+0.5) ||
				validateFloat(a.Validators, *p.ExclusiveMinimum) || !validateFloat(a.Validators, *p.ExclusiveMinimum+0.001) {
				t.Errorf("%s.%s: validators do not match the API bounds (%g, %g]", name, k, *p.ExclusiveMinimum, *p.Maximum)
			}
		case schema.StringAttribute:
			switch {
			case len(p.Enum) > 0:
				for _, v := range p.Enum {
					if !validateString(a.Validators, v) {
						t.Errorf("%s.%s: %q is allowed by the API but refused here", name, k, v)
					}
				}
				if validateString(a.Validators, "bogus") {
					t.Errorf("%s.%s: accepts values outside the API's choices %v", name, k, p.Enum)
				}
			case p.Pattern != "":
				for _, v := range []string{"00:00", "09:30", "23:59"} {
					if !validateString(a.Validators, v) {
						t.Errorf("%s.%s: %q is allowed by the API but refused here", name, k, v)
					}
				}
				for _, v := range []string{"24:00", "9:00", "12:60", "bogus"} {
					if validateString(a.Validators, v) {
						t.Errorf("%s.%s: accepts %q, which the API refuses", name, k, v)
					}
				}
			}
		case schema.BoolAttribute:
			if p.Type != any("boolean") {
				t.Errorf("%s.%s: the API field is %v, not boolean", name, k, p.Type)
			}
		}
	}
}

func TestTelegramSettingsMatchOpenAPI(t *testing.T) {
	schemas := loadOpenAPISchemas(t)
	ctx := context.Background()
	var rs resource.SchemaResponse
	newTelegramSettingsResource().Schema(ctx, resource.SchemaRequest{}, &rs)
	attrs := rs.Schema.Attributes
	props := schemas["SettingsInput"].Properties
	var top = map[string]schema.Attribute{}
	for k, a := range attrs {
		top[k] = a
	}
	// The nested objects are compared below.
	compareAttrs(t, "telegram_settings", top, props, "id", "send_limits", "inbound_limits", "notification_defaults")
	compareAttrs(t, "send_limits", tgSendLimitsAttrs, schemas["SettingsSendLimitsInput"].Properties)
	compareAttrs(t, "inbound_limits", tgInboundLimitsAttrs, schemas["SettingsInboundLimitsInput"].Properties)
	compareAttrs(t, "notification_defaults", tgNotificationAttrs, schemas["SettingsNotificationDefaultsInput"].Properties)
	for _, k := range []string{"send_limits", "inbound_limits", "notification_defaults"} {
		if _, ok := attrs[k]; !ok || props[k].Ref == "" {
			t.Errorf("%s is missing on one side", k)
		}
	}

	var sr resource.SchemaResponse
	newSpaceTelegramResource().Schema(ctx, resource.SchemaRequest{}, &sr)
	compareAttrs(t, "space_telegram", sr.Schema.Attributes, schemas["SettingsSpaceInput"].Properties, "id", "space_id")
}
