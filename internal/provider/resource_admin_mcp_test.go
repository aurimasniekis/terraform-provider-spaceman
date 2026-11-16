package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// reissues: grants or expiry changes re-issue the token, including drift of never_expires alone; an imported expiring
// server adopts a configured expires_in_days without a re-issue.
func TestAdminMCPReissues(t *testing.T) {
	days := func(v int64) types.Int64 { return types.Int64Value(v) }
	none := types.Int64Null()
	model := func(d types.Int64, never bool) *adminMCPServerModel {
		return &adminMCPServerModel{Grants: adminMCPDefaultGrants(), ExpiresInDays: d, NeverExpires: types.BoolValue(never)}
	}
	cases := []struct {
		name        string
		plan, prior *adminMCPServerModel
		want        bool
	}{
		{"unchanged never", model(none, true), model(none, true), false},
		{"unchanged days", model(days(30), false), model(days(30), false), false},
		{"days changed", model(days(60), false), model(days(30), false), true},
		{"to never", model(none, true), model(days(30), false), true},
		{"out-of-band expiry on a never-expiring server", model(none, true), model(none, false), true},
		{"out-of-band never on an expiring server", model(days(30), false), model(days(30), true), true},
		{"import adopts days", model(days(30), false), model(none, false), false},
		{"grants changed", &adminMCPServerModel{Grants: grantsValue(nil), ExpiresInDays: none, NeverExpires: types.BoolValue(true)}, model(none, true), true},
	}
	r := &adminMCPServerResource{}
	for _, c := range cases {
		if got := r.reissues(c.plan, c.prior); got != c.want {
			t.Errorf("%s: reissues = %v, want %v", c.name, got, c.want)
		}
	}
}
