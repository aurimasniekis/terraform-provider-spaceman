package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// ingest_own: create sends it only when on; update sends it explicitly (false turns it off) and leaves it out when nil;
// a subscription read from a central that predates the field reads as off.
func TestSubscriptionIngestOwn(t *testing.T) {
	f := &fakeAPI{resp: []func(http.ResponseWriter){
		jsonResp(200, map[string]any{"id": "sub_1", "ingest_own": true, "version": 1}),
		jsonResp(200, map[string]any{"id": "sub_1", "ingest_own": false, "version": 2}),
		jsonResp(200, map[string]any{"id": "sub_1", "version": 3}),
		jsonResp(200, map[string]any{"id": "sub_1", "version": 4}),
	}}
	c := newTest(t, f)
	ctx := context.Background()
	on, off := true, false
	s, err := c.CreateSubscription(ctx, "spc_1", CreateSubscriptionInput{Name: "n", ConnectionID: "con_1", IngestOwn: true})
	if err != nil || !s.IngestOwn {
		t.Fatalf("create: %v %+v", err, s)
	}
	if s, err = c.UpdateSubscription(ctx, "spc_1", "sub_1", 1, UpdateSubscriptionInput{IngestOwn: &off}); err != nil || s.IngestOwn {
		t.Fatalf("update off: %v %+v", err, s)
	}
	if s, err = c.UpdateSubscription(ctx, "spc_1", "sub_1", 2, UpdateSubscriptionInput{IngestOwn: &on}); err != nil || s.IngestOwn {
		t.Fatalf("update on, old central: %v %+v", err, s)
	}
	if _, err = c.UpdateSubscription(ctx, "spc_1", "sub_1", 3, UpdateSubscriptionInput{}); err != nil {
		t.Fatal(err)
	}
	if b := f.reqs[0].body; !strings.Contains(b, `"ingest_own":true`) {
		t.Fatalf("create body: %s", b)
	}
	if b := f.reqs[1].body; !strings.Contains(b, `"ingest_own":false`) {
		t.Fatalf("update off body: %s", b)
	}
	if b := f.reqs[2].body; !strings.Contains(b, `"ingest_own":true`) {
		t.Fatalf("update on body: %s", b)
	}
	if b := f.reqs[3].body; strings.Contains(b, "ingest_own") {
		t.Fatalf("nil must be omitted: %s", b)
	}
}
