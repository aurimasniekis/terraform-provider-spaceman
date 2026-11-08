package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The Harbor Pilot connection: create without If-Match, replace and delete with it; empty tokens are not sent, and a
// delete of a missing connection counts as done.
func TestHarborPilotConnectionIfMatch(t *testing.T) {
	f := &fakeAPI{resp: []func(http.ResponseWriter){
		jsonResp(200, map[string]any{"id": "hpc_1", "version": 1}),
		jsonResp(200, map[string]any{"id": "hpc_1", "version": 2}),
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
		problem(404, "not_found", "no Harbor Pilot connection is configured"),
	}}
	c := newTest(t, f)
	ctx := context.Background()
	if _, err := c.PutHarborPilotConnection(ctx, 0, HarborPilotConnectionInput{BaseURL: "https://hp.example.com", ParentToken: "hpk_x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutHarborPilotConnection(ctx, 1, HarborPilotConnectionInput{BaseURL: "https://hp.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteHarborPilotConnection(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteHarborPilotConnection(ctx, 2); err != nil {
		t.Fatalf("a missing connection: %v", err)
	}
	if r := f.reqs[0]; r.ifMatch != "" || r.method != http.MethodPut || r.path != "/api/v1/harbor-pilot/connection" || !strings.Contains(r.body, `"parent_token":"hpk_x"`) {
		t.Fatalf("create: %+v", r)
	}
	if r := f.reqs[1]; r.ifMatch != `"1"` || strings.Contains(r.body, "parent_token") || strings.Contains(r.body, "service_token") {
		t.Fatalf("update: %+v", r)
	}
	if r := f.reqs[2]; r.method != http.MethodDelete || r.ifMatch != `"2"` {
		t.Fatalf("delete: %+v", r)
	}
}

// A space's settings always send allowed_model_aliases (an empty list clears them) and If-Match on replace and delete.
func TestSpaceHarborPilotIfMatch(t *testing.T) {
	f := &fakeAPI{resp: []func(http.ResponseWriter){
		jsonResp(200, map[string]any{"space_id": "spc_1", "stored": true, "version": 3}),
		func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
	}}
	c := newTest(t, f)
	ctx := context.Background()
	if _, err := c.PutSpaceHarborPilot(ctx, "spc_1", 2, SpaceHarborPilotInput{Enabled: true, ProjectID: "prj_1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSpaceHarborPilot(ctx, "spc_1", 3); err != nil {
		t.Fatal(err)
	}
	if r := f.reqs[0]; r.ifMatch != `"2"` || r.path != "/api/v1/spaces/spc_1/harbor-pilot" || !strings.Contains(r.body, `"allowed_model_aliases":[]`) {
		t.Fatalf("put: %+v", r)
	}
	if r := f.reqs[1]; r.method != http.MethodDelete || r.ifMatch != `"3"` {
		t.Fatalf("delete: %+v", r)
	}
}
