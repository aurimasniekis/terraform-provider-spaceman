package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

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

func TestSupergroupChatIDValidator(t *testing.T) {
	for _, c := range []struct {
		in      int64
		wantErr bool
	}{{-1001234567890, false}, {0, false}, {5, true}} {
		resp := &validator.Int64Response{}
		supergroupChatID{}.ValidateInt64(context.Background(),
			validator.Int64Request{Path: path.Root("group_chat_id"), ConfigValue: types.Int64Value(c.in)}, resp)
		if resp.Diagnostics.HasError() != c.wantErr {
			t.Errorf("%d: error = %v, want %v", c.in, resp.Diagnostics.HasError(), c.wantErr)
		}
		if c.wantErr && !strings.Contains(resp.Diagnostics[0].Detail(), "a negative supergroup chat ID, or 0 to unbind") {
			t.Errorf("message = %q", resp.Diagnostics[0].Detail())
		}
	}
}

// fakeSpaceTelegram answers GET and PUT /spaces/spc_1/telegram; a PUT fails with status/code when status is not 0, and
// every PUT body is recorded.
type fakeSpaceTelegram struct {
	mu     sync.Mutex
	puts   []map[string]any
	status int
	code   string
}

func (f *fakeSpaceTelegram) start(t *testing.T) *spaceTelegramResource {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPut && f.status != 0 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(f.status)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": f.status, "code": f.code, "detail": "boom"})
			return
		}
		if r.Method == http.MethodPut {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.puts = append(f.puts, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"space_id": "spc_1", "stored": true, "enabled": true, "content_level": "full",
			"enforced_approvals": "web_only", "group_chat_id": -1001})
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "tok", client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return &spaceTelegramResource{base{c: c}}
}

func telegramModel(group types.Int64) spaceTelegramModel {
	return spaceTelegramModel{SpaceID: types.StringValue("spc_1"), Enabled: types.BoolValue(true), ContentLevel: types.StringValue("full"),
		EnforcedApprovals: types.StringValue("web_only"), Topics: types.BoolValue(false), TerminalWatch: types.BoolValue(false), GroupChatID: group}
}

// A 409 conflict while a group is sent is "the group is bound elsewhere", on group_chat_id; a 503 stays generic.
func TestSpaceTelegramSaveGroupErrors(t *testing.T) {
	g := int64(-1001)
	for _, c := range []struct {
		name, code, wantSummary, wantDetail string
		status                              int
		group                               *int64
		wantAttr                            bool
	}{
		{"conflict", "conflict", "The group is bound to another space", "unbind it there first", 409, &g, true},
		{"conflict without a group", "conflict", "Could not save", "Run `terraform plan` again", 409, nil, false},
		{"unavailable", "unavailable", "Could not save", "unavailable", 503, &g, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeSpaceTelegram{status: c.status, code: c.code}
			r := f.start(t)
			m := telegramModel(types.Int64Value(-1001))
			var d diagnostics
			if _, ok := r.save(context.Background(), &m, 1, c.group, &d); ok || len(d) != 1 {
				t.Fatalf("ok=%v diagnostics=%v", ok, d)
			}
			if !strings.Contains(d[0].Summary(), c.wantSummary) || !strings.Contains(d[0].Detail(), c.wantDetail) {
				t.Errorf("diagnostic = %q / %q", d[0].Summary(), d[0].Detail())
			}
			if hint := strings.Contains(d[0].Detail(), "changed in SpaceMan"); hint != (c.status == 409 && !c.wantAttr) {
				t.Errorf("generic re-plan hint = %v for %s: %q", hint, c.name, d[0].Detail())
			}
			_, isAttr := d[0].(interface{ Path() path.Path })
			if isAttr != c.wantAttr {
				t.Errorf("attribute diagnostic = %v, want %v", isAttr, c.wantAttr)
			}
		})
	}
}

// An unchanged group_chat_id is not sent again (only a change needs telegram_group:write); a changed one is.
func TestSpaceTelegramUpdateResendsOnlyChangedGroup(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name        string
		prior, plan int64
		wantSent    bool
	}{{"unchanged", -1001, -1001, false}, {"changed", -1001, 0, true}} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeSpaceTelegram{}
			r := f.start(t)
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)
			state, plan := tfsdk.State{Schema: sr.Schema}, tfsdk.Plan{Schema: sr.Schema}
			prior, next := telegramModel(types.Int64Value(c.prior)), telegramModel(types.Int64Value(c.plan))
			prior.ID, next.ID = types.StringValue("spc_1"), types.StringValue("spc_1")
			if d := state.Set(ctx, &prior); d.HasError() {
				t.Fatal(d)
			}
			if d := plan.Set(ctx, &next); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if len(f.puts) != 1 {
				t.Fatalf("PUTs = %d", len(f.puts))
			}
			_, sent := f.puts[0]["group_chat_id"]
			if sent != c.wantSent {
				t.Errorf("group_chat_id sent = %v, want %v (body %v)", sent, c.wantSent, f.puts[0])
			}
		})
	}
}
