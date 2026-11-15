package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCanonicalCIDRValidator(t *testing.T) {
	cases := []struct {
		in      string
		wantErr string // "" = accepted
	}{
		{"149.154.160.0/20", ""},
		{"2001:db8::/32", ""},
		{"10.0.0.1/8", "canonical"},
		{"nonsense", "canonical"},
		{"::ffff:149.154.160.0/116", "IPv4-mapped"},
		{"::ffff:0.0.0.0/96", "IPv4-mapped"},
	}
	for _, c := range cases {
		resp := &validator.StringResponse{}
		canonicalCIDR{}.ValidateString(context.Background(),
			validator.StringRequest{Path: path.Root("x"), ConfigValue: types.StringValue(c.in)}, resp)
		got := ""
		if resp.Diagnostics.HasError() {
			e := resp.Diagnostics.Errors()[0]
			got = e.Summary() + " " + e.Detail()
		}
		switch {
		case c.wantErr == "" && got != "":
			t.Errorf("%q: unexpected error %s", c.in, got)
		case c.wantErr != "" && !strings.Contains(got, c.wantErr):
			t.Errorf("%q: error %q, want it to mention %q", c.in, got, c.wantErr)
		}
	}
}

func TestMiniAppHostError(t *testing.T) {
	label63 := strings.Repeat("a", 63)
	long := strings.Join([]string{label63, label63, label63, label63}, ".") // 255 chars
	cases := []struct {
		host    string
		wantErr string
	}{
		{"", ""},
		{"tg.central.example.com", ""},
		{"a-b.example.com", ""},
		{"1.example.com", ""},
		{"127.0.0.1", "IP address"},
		{"::1", "IP address"},
		{"[::1]", "IP address"},
		{"https://tg.example.com", "bare host"},
		{"tg.example.com:443", "bare host"},
		{"tg.example.com/path", "bare host"},
		{"tg.example.com?x", "bare host"},
		{"tg.example.com#x", "bare host"},
		{"user@tg.example.com", "bare host"},
		{"tg example.com", "bare host"},
		{"TG.example.com", "lower case"},
		{"localhost", "fully qualified"},
		{long, "fully qualified"},
		{"-tg.example.com", "valid DNS name"},
		{"tg-.example.com", "valid DNS name"},
		{"tg_x.example.com", "valid DNS name"},
		{"tg..example.com", "valid DNS name"},
		{"tg.example.com.", "valid DNS name"}, // trailing dot: an empty last label
		{label63 + "a.example.com", "valid DNS name"},
		{"tg.123", "end in a number"},
		{"tg.0x7f", "end in a number"},
		{"tg.0x", "end in a number"},
	}
	for _, c := range cases {
		got := miniAppHostError(c.host)
		switch {
		case c.wantErr == "" && got != "":
			t.Errorf("%q: unexpected error %q", c.host, got)
		case c.wantErr != "" && !strings.Contains(got, c.wantErr):
			t.Errorf("%q: error %q, want it to mention %q", c.host, got, c.wantErr)
		}
	}
}

func TestGroupChatIDPtr(t *testing.T) {
	if groupChatIDPtr(types.Int64Null()) != nil || groupChatIDPtr(types.Int64Unknown()) != nil {
		t.Fatal("null and unknown must send nothing, so that central leaves the binding alone")
	}
	if p := groupChatIDPtr(types.Int64Value(0)); p == nil || *p != 0 {
		t.Fatalf("0 must be sent (it unbinds): %v", p)
	}
	if p := groupChatIDPtr(types.Int64Value(-1001)); p == nil || *p != -1001 {
		t.Fatalf("got %v", p)
	}
}
