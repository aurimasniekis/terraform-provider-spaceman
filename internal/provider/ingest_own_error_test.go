package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

// A SpaceMan that predates ingest_own answers 422 "unexpected property" for it. The error then names the version needed,
// and only when the request sent the field.
func TestSubscriptionIngestOwnRejectedHint(t *testing.T) {
	rejected := &client.APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Title: "Unprocessable Entity",
		Detail: "invalid request", Operation: "POST /spaces/spc_1/subscriptions",
		Errors: []client.FieldError{{Field: "body.ingest_own", Message: "unexpected property"}}}
	other := &client.APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Detail: "invalid request",
		Operation: "POST /spaces/spc_1/subscriptions", Errors: []client.FieldError{{Field: "body.name", Message: "is required"}}}
	for _, c := range []struct {
		name     string
		err      error
		sent     bool
		wantHint bool
	}{
		{"rejected, sent", rejected, true, true},
		{"rejected, not sent", rejected, false, false},
		{"other 422, sent", other, true, false},
		{"not an API error", errors.New("boom"), true, false},
	} {
		var d diag.Diagnostics
		addSubscriptionAPIError(&d, "Could not create the subscription", c.err, c.sent)
		errs := d.Errors()
		if len(errs) != 1 {
			t.Fatalf("%s: got %d errors, want 1", c.name, len(errs))
		}
		if errs[0].Summary() != "Could not create the subscription" {
			t.Errorf("%s: summary %q", c.name, errs[0].Summary())
		}
		if hint := strings.Contains(errs[0].Detail(), "needs SpaceMan 0.1.16"); hint != c.wantHint {
			t.Errorf("%s: hint %v, want %v (detail %q)", c.name, hint, c.wantHint, errs[0].Detail())
		}
	}
}
