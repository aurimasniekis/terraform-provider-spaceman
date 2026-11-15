package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The Telegram settings schemas are kept in step with SpaceMan's API: the attributes and the bounds of every value are
// compared with the OpenAPI document of the SpaceMan checkout (SPACEMAN_SRC, or ../spaceman). The schemas are found
// through the request bodies of the operations, not by Go type names. Without a checkout, or with one that predates
// the settings operations, the test is skipped, unless SM_REQUIRE_OPENAPI=1 (set in CI), which turns every skip into a
// failure: a pipeline that no longer compares anything must not stay green.

type oaProp struct {
	Type             any      `json:"type"`
	Minimum          *float64 `json:"minimum"`
	Maximum          *float64 `json:"maximum"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum"`
	Enum             []string `json:"enum"`
	Pattern          string   `json:"pattern"`
	Ref              string   `json:"$ref"`
}

type oaSchema struct {
	Properties map[string]oaProp `json:"properties"`
}

type oaDoc struct {
	Paths map[string]map[string]struct {
		RequestBody struct {
			Content map[string]struct {
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"content"`
		} `json:"requestBody"`
	} `json:"paths"`
	Components struct {
		Schemas map[string]oaSchema `json:"schemas"`
	} `json:"components"`
}

// skipOrFail skips the test, or fails it when SM_REQUIRE_OPENAPI is set.
func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("SM_REQUIRE_OPENAPI") != "" {
		t.Fatalf("SM_REQUIRE_OPENAPI is set, but the contract cannot be checked: "+format, args...)
	}
	t.Skipf(format, args...)
}

func loadOpenAPIDoc(t *testing.T) *oaDoc {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		skipOrFail(t, "no SpaceMan checkout: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "api", "openapi.json"))
	if err != nil {
		skipOrFail(t, "no OpenAPI document: %v", err)
	}
	var doc oaDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

// requestSchema is the schema the PUT of an API path takes as its JSON body, found through the body's $ref.
func (d *oaDoc) requestSchema(t *testing.T, apiPath string) oaSchema {
	t.Helper()
	op, ok := d.Paths[apiPath]["put"]
	if !ok {
		skipOrFail(t, "the SpaceMan checkout has no PUT %s", apiPath)
	}
	return d.schemaByRef(t, op.RequestBody.Content["application/json"].Schema.Ref, "PUT "+apiPath)
}

func (d *oaDoc) schemaByRef(t *testing.T, ref, what string) oaSchema {
	t.Helper()
	const prefix = "#/components/schemas/"
	name, found := strings.CutPrefix(ref, prefix)
	sc, ok := d.Components.Schemas[name]
	if !found || !ok {
		t.Fatalf("%s: the schema %q cannot be resolved", what, ref)
	}
	return sc
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
			if k == "group_chat_id" {
				continue // a chat ID: the API only says "negative, or 0" in prose; the validator rejects positive IDs early
			}
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
	doc := loadOpenAPIDoc(t)
	ctx := context.Background()
	var rs resource.SchemaResponse
	newTelegramSettingsResource().Schema(ctx, resource.SchemaRequest{}, &rs)
	attrs := rs.Schema.Attributes
	props := doc.requestSchema(t, "/telegram/settings").Properties
	var top = map[string]schema.Attribute{}
	for k, a := range attrs {
		top[k] = a
	}
	// The nested objects are compared through the $ref of their field.
	nested := func(field string) map[string]oaProp {
		return doc.schemaByRef(t, props[field].Ref, field).Properties
	}
	compareAttrs(t, "telegram_settings", top, props, "id", "send_limits", "inbound_limits", "notification_defaults")
	compareAttrs(t, "send_limits", tgSendLimitsAttrs, nested("send_limits"))
	compareAttrs(t, "inbound_limits", tgInboundLimitsAttrs, nested("inbound_limits"))
	compareAttrs(t, "notification_defaults", tgNotificationAttrs, nested("notification_defaults"))
	for _, k := range []string{"send_limits", "inbound_limits", "notification_defaults"} {
		if _, ok := attrs[k]; !ok || props[k].Ref == "" {
			t.Errorf("%s is missing on one side", k)
		}
	}

	var sr resource.SchemaResponse
	newSpaceTelegramResource().Schema(ctx, resource.SchemaRequest{}, &sr)
	compareAttrs(t, "space_telegram", sr.Schema.Attributes, doc.requestSchema(t, "/spaces/{space_id}/telegram").Properties, "id", "space_id")
}
