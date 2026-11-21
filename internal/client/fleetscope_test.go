package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The grant is PUT (idempotent, no If-Match), read with GET and revoked with DELETE; a 409 for an archived space and a
// 404 on revoke are told apart.
func TestFleetScopeGrant(t *testing.T) {
	f := &fakeAPI{resp: []func(http.ResponseWriter){
		jsonResp(200, map[string]any{"space_id": "spc_1", "reason": "ops", "granted_at": "2026-11-21T10:00:00Z", "granted_by": "usr_1", "fleet_subscriptions": 2}),
		jsonResp(200, map[string]any{"space_id": "spc_1", "granted_at": "2026-11-21T10:00:00Z", "granted_by": "usr_1", "fleet_subscriptions": 0}),
		problem(409, "precondition_failed", "space is archived"),
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
		problem(404, "not_found", "no grant"),
	}}
	c := newTest(t, f)
	ctx := context.Background()
	g, err := c.PutFleetScopeGrant(ctx, "spc_1", FleetScopeInput{Reason: "ops"})
	if err != nil || g.Reason != "ops" || g.FleetSubscriptions != 2 || g.GrantedBy != "usr_1" {
		t.Fatalf("put: %v %+v", err, g)
	}
	if g, err = c.GetFleetScopeGrant(ctx, "spc_1"); err != nil || g.Reason != "" {
		t.Fatalf("get: %v %+v", err, g)
	}
	if _, err = c.PutFleetScopeGrant(ctx, "spc_2", FleetScopeInput{}); !IsConflict(err) {
		t.Fatalf("archived: want 409, got %v", err)
	}
	if err = c.DeleteFleetScopeGrant(ctx, "spc_1"); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteFleetScopeGrant(ctx, "spc_1"); err != nil {
		t.Fatalf("revoking a missing grant counts as done: %v", err)
	}
	if r := f.reqs[0]; r.method != http.MethodPut || r.path != "/api/v1/event-fleet-scope-grants/spc_1" || !strings.Contains(r.body, `"reason":"ops"`) {
		t.Fatalf("put request: %+v", r)
	}
	if b := f.reqs[2].body; strings.Contains(b, "reason") {
		t.Fatalf("empty reason must be omitted: %s", b)
	}
}

// ingest_own_scope: create sends it only as fleet; update leaves it out when nil; a central before 0.1.17 reads as empty.
func TestSubscriptionIngestOwnScope(t *testing.T) {
	f := &fakeAPI{resp: []func(http.ResponseWriter){
		jsonResp(200, map[string]any{"id": "sub_1", "ingest_own": true, "ingest_own_scope": "fleet", "version": 1}),
		jsonResp(200, map[string]any{"id": "sub_1", "ingest_own": true, "version": 2}),
		jsonResp(200, map[string]any{"id": "sub_1", "version": 3}),
	}}
	c := newTest(t, f)
	ctx := context.Background()
	s, err := c.CreateSubscription(ctx, "spc_1", CreateSubscriptionInput{Name: "n", ConnectionID: "con_1", IngestOwn: true, IngestOwnScope: "fleet"})
	if err != nil || s.IngestOwnScope != "fleet" {
		t.Fatalf("create: %v %+v", err, s)
	}
	back := "space"
	if s, err = c.UpdateSubscription(ctx, "spc_1", "sub_1", 1, UpdateSubscriptionInput{IngestOwnScope: &back}); err != nil || s.IngestOwnScope != "" {
		t.Fatalf("update: %v %+v", err, s)
	}
	if _, err = c.UpdateSubscription(ctx, "spc_1", "sub_1", 2, UpdateSubscriptionInput{}); err != nil {
		t.Fatal(err)
	}
	if b := f.reqs[0].body; !strings.Contains(b, `"ingest_own_scope":"fleet"`) {
		t.Fatalf("create body: %s", b)
	}
	if b := f.reqs[1].body; !strings.Contains(b, `"ingest_own_scope":"space"`) {
		t.Fatalf("update body: %s", b)
	}
	if b := f.reqs[2].body; strings.Contains(b, "ingest_own_scope") {
		t.Fatalf("nil must be omitted: %s", b)
	}
	b, _ := json.Marshal(CreateSubscriptionInput{Name: "n"})
	if strings.Contains(string(b), "ingest_own_scope") {
		t.Fatalf("default create must not send the scope: %s", b)
	}
}
