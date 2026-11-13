package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---- Claude Code version pin on harness profiles (SpaceMan ADR-0077) ----

// reHarnessVersion is SpaceMan's pin syntax: empty (follow the node's channel), latest, stable or a strict X.Y.Z with
// no leading zeros and no pre-release or build suffix (go-spaceman proto.CheckHarnessVersion).
var reHarnessVersion = regexp.MustCompile(`^(|latest|stable|(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5}))$`)

// harnessVersionAttr is Optional+Computed: the state always holds the server's pin, and a configuration without the
// attribute keeps whatever pin SpaceMan has (the provider leaves the field out of the request), so it never shows as
// drift. Only a configured value is enforced, and `""` clears the pin.
func harnessVersionAttr() schema.StringAttribute {
	return schema.StringAttribute{Optional: true, Computed: true,
		Validators: []validatorString{stringvalidator.RegexMatches(reHarnessVersion,
			`must be "" (follow the node's channel), "latest", "stable" or an exact version such as "2.1.3"`)},
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		MarkdownDescription: "The Claude Code version the profile's runs get (`claude-code` profiles only; SpaceMan ADR-0077): `latest`, " +
			"`stable` or an exact `X.Y.Z` (no leading zeros, no suffix). `\"\"` clears the pin, so runs follow the node's channel. " +
			"When the attribute is left out of the configuration the provider does not send it, so a pin set elsewhere (the web " +
			"UI, the API) is kept and read into the state without showing as a change; removing the attribute from the " +
			"configuration likewise keeps the current pin. Set it (to a value or `\"\"`) to have Terraform enforce it. Requires " +
			"SpaceMan 0.1.2 or later."}
}

// configuredHarnessVersion is the API value for the configured pin: nil (keep the stored pin) when the configuration
// leaves the attribute out or it is not known yet, else the value; `""` clears the pin.
func configuredHarnessVersion(ctx context.Context, config tfsdk.Config, diags *diag.Diagnostics) *string {
	var v types.String
	diags.Append(config.GetAttribute(ctx, path.Root("harness_version"), &v)...)
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

// checkHarnessVersionHarness refuses a non-empty pin on a profile whose harness is not claude-code (SpaceMan refuses it
// too, but only at apply time).
func checkHarnessVersionHarness(harness, version types.String, diags *diag.Diagnostics) {
	if harness.IsNull() || harness.IsUnknown() || version.IsNull() || version.IsUnknown() {
		return
	}
	if version.ValueString() != "" && harness.ValueString() != "claude-code" {
		diags.AddAttributeError(path.Root("harness_version"), "harness_version needs a claude-code profile",
			"Only claude-code profiles can pin a harness version; this profile's harness is "+harness.ValueString()+". Remove harness_version.")
	}
}
