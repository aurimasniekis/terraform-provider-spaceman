package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// An update sends ingest_own only when it is on in the plan or was on in the prior state, so a SpaceMan that predates
// the field (and rejects unknown properties) keeps accepting updates.
func TestIngestOwnPatch(t *testing.T) {
	on, off, null := types.BoolValue(true), types.BoolValue(false), types.BoolNull()
	for _, c := range []struct {
		name        string
		plan, prior types.Bool
		want        *bool // nil: left out of the body
		wantVal     bool
	}{
		{"off to off", off, off, nil, false},
		{"unset to off", off, null, nil, false},
		{"on to on", on, on, ptr(true), true},
		{"off to on", on, off, ptr(true), true},
		{"on to off", off, on, ptr(false), false},
	} {
		got := ingestOwnPatch(c.plan, c.prior)
		if (got == nil) != (c.want == nil) || (got != nil && *got != c.wantVal) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func ptr(b bool) *bool { return &b }
