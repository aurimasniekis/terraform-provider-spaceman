package provider

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// ---- Claude Code settings on harness profiles (SpaceMan ADR-0059) ----

func claudeSettingsAttr() schema.StringAttribute {
	return schema.StringAttribute{Optional: true, CustomType: jsontypes.NormalizedType{}, Validators: []validator.String{jsonObjectValidator{}},
		MarkdownDescription: "Claude Code `settings.json` keys for the profile's runs, as a JSON object (`jsonencode({...})`): any keys, such as " +
			"`permissions`, `autoMode`, `env` or `hooks` (`claude-code` profiles only; ADR-0059). SpaceMan sets them in " +
			"each run's own `settings.json` at launch. `permissions.defaultMode` is held to central's permission mode ceiling. Hooks run commands on " +
			"the node, and the settings are stored in clear, so keep secrets out. The server's key order never causes a diff. Removing the attribute " +
			"or setting `jsonencode({})` clears the settings; SpaceMan stores both as no settings, so an import reads either as unset. A warning " +
			"flags `env` keys that look like credential or endpoint overrides (`ANTHROPIC_*`, `CLAUDE_CODE_*`, `*_PROXY`)."}
}

// claudeSettingsInput is the API value for the configured settings: nil (keep what is stored) only when neither the
// configuration nor the prior state has any, so a server without the field never sees it; `{}` clears them.
func claudeSettingsInput(v, prior jsontypes.Normalized) json.RawMessage {
	if v.IsUnknown() {
		return nil
	}
	if v.IsNull() || strings.TrimSpace(v.ValueString()) == "" {
		if prior.IsNull() || prior.IsUnknown() {
			return nil
		}
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(v.ValueString())
}

// claudeSettingsValue is the state value for the server's settings, given the value the plan or state holds: the
// server's JSON when it has settings, else the held value when that is an empty object (`{}` is stored as no settings,
// so keeping it keeps the apply consistent with the plan), else null. The normalized type keeps the held value when the
// two are semantically equal, so the server's key order and formatting never cause a diff.
func claudeSettingsValue(server json.RawMessage, held jsontypes.Normalized) jsontypes.Normalized {
	if !isEmptyJSONObject(server) {
		return jsontypes.NewNormalizedValue(string(server))
	}
	if !held.IsNull() && !held.IsUnknown() && isEmptyJSONObject(json.RawMessage(held.ValueString())) {
		return held
	}
	return jsontypes.NewNormalizedNull()
}

// isEmptyJSONObject reports whether v is absent, not a JSON object, or an object without keys.
func isEmptyJSONObject(v json.RawMessage) bool {
	var m map[string]any
	return len(v) == 0 || json.Unmarshal(v, &m) != nil || len(m) == 0
}

// jsonObjectValidator accepts a JSON object.
type jsonObjectValidator struct{}

func (jsonObjectValidator) Description(context.Context) string { return "must be a JSON object" }
func (v jsonObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (jsonObjectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &m); err != nil || m == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Not a JSON object", "claude_settings must be a JSON object, for example jsonencode({ permissions = { deny = [\"Read(./secrets/**)\"] } }).")
		return
	}
	if env, ok := m["env"].(map[string]any); ok {
		if keys := sensitiveEnvKeys(env); len(keys) > 0 {
			resp.Diagnostics.AddAttributeWarning(req.Path, "claude_settings.env overrides credentials or endpoints",
				"claude_settings.env sets "+strings.Join(keys, ", ")+". Variables such as ANTHROPIC_*, CLAUDE_CODE_* and *_PROXY can "+
					"change where Claude Code sends requests or which credentials it uses, and the settings are stored in clear: "+
					"set them only if that is intended.")
		}
	}
}

// sensitiveEnvKeys lists, sorted, the env keys that look like a credential or endpoint override: ANTHROPIC_*,
// CLAUDE_CODE_* or *_PROXY (any case).
func sensitiveEnvKeys(env map[string]any) []string {
	var out []string
	for k := range env {
		u := strings.ToUpper(k)
		if strings.HasPrefix(u, "ANTHROPIC_") || strings.HasPrefix(u, "CLAUDE_CODE_") || strings.HasSuffix(u, "_PROXY") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
