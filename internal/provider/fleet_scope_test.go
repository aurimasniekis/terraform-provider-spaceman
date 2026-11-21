package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// An update sends ingest_own_scope only when it is fleet in the plan or was fleet before (leaving it), so a SpaceMan
// before 0.1.17, which rejects unknown properties, keeps accepting updates of configurations that do not set it.
func TestIngestOwnScopePatch(t *testing.T) {
	s := types.StringValue
	for _, c := range []struct {
		name        string
		plan, prior types.String
		want        string // "": left out of the body
	}{
		{"space to space", s("space"), s("space"), ""},
		{"unset to space", s("space"), types.StringNull(), ""},
		{"fleet to fleet", s("fleet"), s("fleet"), "fleet"},
		{"space to fleet", s("fleet"), s("space"), "fleet"},
		{"fleet to space", s("space"), s("fleet"), "space"},
	} {
		got := ingestOwnScopePatch(c.plan, c.prior)
		if c.want == "" && got != nil || c.want != "" && (got == nil || *got != c.want) {
			t.Errorf("%s: got %v, want %q", c.name, got, c.want)
		}
	}
}

func TestSubscriptionScopeErrors(t *testing.T) {
	rejected := &client.APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Detail: "invalid request",
		Operation: "POST /spaces/spc_1/subscriptions", Errors: []client.FieldError{{Field: "body.ingest_own_scope", Message: "unexpected property"}}}
	forbidden := &client.APIError{Status: http.StatusForbidden, Code: "forbidden", Detail: "this space holds no fleet-scope grant", Operation: "POST /spaces/spc_1/subscriptions"}
	for _, c := range []struct {
		name string
		err  error
		sent bool
		want string // substring of the detail
	}{
		{"older server", rejected, true, "ingest_own_scope needs SpaceMan 0.1.17"},
		{"no grant", forbidden, true, "fleet-scope grant"},
		{"403 without scope", forbidden, false, "lacks the permission"},
		{"not an API error", errors.New("boom"), true, "boom"},
	} {
		var d diag.Diagnostics
		addSubscriptionAPIError(&d, "Could not create the subscription", c.err, false, c.sent)
		if errs := d.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Detail(), c.want) {
			t.Errorf("%s: %v, want detail containing %q", c.name, errs, c.want)
		}
	}
}

// A grant on an archived space answers 409 precondition_failed: the diagnostic says so, and a refused token is pointed at
// the operator-level requirement. Another 409 is not mislabelled as an archived space.
func TestFleetScopeGrantErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		code   string
		want   string
		absent string // substring the detail must not contain
	}{
		{"archived", http.StatusConflict, "precondition_failed", "space is archived", ""},
		{"other conflict", http.StatusConflict, "conflict", "409", "archived"},
		{"forbidden", http.StatusForbidden, "forbidden", "operator-level token", ""},
		{"unauthorized", http.StatusUnauthorized, "unauthorized", "operator-level token", ""},
		{"server error", http.StatusInternalServerError, "internal", "500", "archived"},
	} {
		var d diag.Diagnostics
		addFleetScopeAPIError(&d, "Could not create the fleet-scope grant", &client.APIError{Status: c.status, Code: c.code, Operation: "PUT /event-fleet-scope-grants/spc_1"})
		errs := d.Errors()
		if len(errs) != 1 || !strings.Contains(errs[0].Detail(), c.want) || (c.absent != "" && strings.Contains(errs[0].Detail(), c.absent)) {
			t.Errorf("%s: %v, want detail containing %q and not %q", c.name, errs, c.want, c.absent)
		}
	}
	var d diag.Diagnostics
	addFleetScopeAPIError(&d, "x", errors.New("boom"))
	if len(d.Errors()) != 1 {
		t.Errorf("plain error: %v", d)
	}
}

// SpaceMan trims the grant reason. A configured reason that differs from the stored one only in surrounding whitespace
// stays as configured (no diff, no "inconsistent result"); a real change is taken from the server.
func TestFleetScopeGrantReasonWhitespace(t *testing.T) {
	s := types.StringValue
	for _, c := range []struct {
		name       string
		configured types.String
		stored     string
		want       types.String
	}{
		{"padded matches server", s("  ops  "), "ops", s("  ops  ")},
		{"padded tabs and newline", s("\tops\n"), "ops", s("\tops\n")},
		{"exact match", s("ops"), "ops", s("ops")},
		{"real change from server", s(" ops "), "other", s("other")},
		{"unset stays null", types.StringNull(), "", types.StringNull()},
		{"empty stays empty", s(""), "", s("")},
		{"whitespace only", s("   "), "", s("   ")},
	} {
		m := fleetScopeGrantModel{SpaceID: s("spc_1"), Reason: c.configured}
		(&fleetScopeGrantResource{}).toModel(&client.FleetScopeGrant{SpaceID: "spc_1", Reason: c.stored}, &m)
		if !m.Reason.Equal(c.want) {
			t.Errorf("%s: reason %v, want %v", c.name, m.Reason, c.want)
		}
	}
}
