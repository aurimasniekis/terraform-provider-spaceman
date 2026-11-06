package provider

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// ---- Claude Code settings on harness profiles (SpaceMan ADR-0059) ----

func claudeSettingsAttr() schema.StringAttribute {
	return schema.StringAttribute{Optional: true, CustomType: jsontypes.NormalizedType{}, Validators: []validator.String{jsonObjectValidator{}},
		MarkdownDescription: "Claude Code `settings.json` keys for the profile's runs, as a JSON object (`jsonencode({...})`): any keys, such as " +
			"`permissions`, `autoMode`, `env` or `hooks` (`claude-code` profiles only; SpaceMan 0.1.0-rc.9 or later, ADR-0059). SpaceMan sets them in " +
			"each run's own `settings.json` at launch. `permissions.defaultMode` is held to central's permission mode ceiling. Hooks run commands on " +
			"the node, and the settings are stored in clear, so keep secrets out. The server's key order never causes a diff. Removing the attribute " +
			"clears the settings."}
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

// claudeSettingsValue is the state value for the server's settings: null for none ({} or absent), else the server's
// JSON. The normalized type keeps the prior value when the two are semantically equal, so the server's key order and
// formatting never cause a diff.
func claudeSettingsValue(server json.RawMessage) jsontypes.Normalized {
	var m map[string]any
	if len(server) == 0 || json.Unmarshal(server, &m) != nil || len(m) == 0 {
		return jsontypes.NewNormalizedNull()
	}
	return jsontypes.NewNormalizedValue(string(server))
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
	}
}
