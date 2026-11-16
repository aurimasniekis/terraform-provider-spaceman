package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-spaceman/internal/client"
)

type poolReq struct{ method, path, ifMatch, body string }

// poolAPI is a fake SpaceMan: GET returns the accounts version, PUTs answer with the queued statuses.
type poolAPI struct {
	mu       sync.Mutex
	reqs     []poolReq
	version  int64
	putCodes []int
}

func (f *poolAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, poolReq{r.Method, r.URL.Path, r.Header.Get("If-Match"), string(b)})
	code := http.StatusOK
	if r.Method == http.MethodGet {
		f.version++
	} else if len(f.putCodes) > 0 {
		code, f.putCodes = f.putCodes[0], f.putCodes[1:]
	}
	v := f.version
	f.mu.Unlock()
	if code != http.StatusOK {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": code, "code": "precondition_failed", "title": "x"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	items := []client.RolePool{}
	if r.Method == http.MethodPut && !strings.Contains(string(b), `"items":[]`) {
		items = append(items, client.RolePool{Role: "worker", Items: []client.PoolEntry{{AccountID: "acc_1", Priority: 10}}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "accounts_version": v})
}

func newPoolResource(t *testing.T, f *poolAPI) *spaceRolePoolResource {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "spaceman_at_test", client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	old := rolePoolBackoff
	rolePoolBackoff = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { rolePoolBackoff = old })
	return &spaceRolePoolResource{base{c: c}}
}

func TestRolePoolPutRetriesOnConflict(t *testing.T) {
	f := &poolAPI{putCodes: []int{http.StatusPreconditionFailed, http.StatusPreconditionFailed, http.StatusOK}}
	r := newPoolResource(t, f)
	got, err := r.put(context.Background(), "spc_1", "worker", []client.PoolEntry{{AccountID: "acc_1", Priority: 10}})
	if err != nil || len(got) != 1 || got[0].AccountID != "acc_1" {
		t.Fatalf("put = %v, %v", got, err)
	}
	var puts []poolReq
	for _, q := range f.reqs {
		if q.method == http.MethodPut {
			puts = append(puts, q)
		}
	}
	if len(puts) != 3 {
		t.Fatalf("PUT attempts = %d, want 3", len(puts))
	}
	// The version is re-read before every attempt (each GET bumps it), and sent as If-Match.
	for i, p := range puts {
		want := `"` + string(rune('1'+i)) + `"`
		if p.ifMatch != want || p.path != "/api/v1/spaces/spc_1/account-pools/worker" {
			t.Fatalf("PUT %d = %+v, want If-Match %s", i, p, want)
		}
	}
}

func TestRolePoolPutGivesUp(t *testing.T) {
	f := &poolAPI{putCodes: []int{412, 412, 412, 412, 412, 412}}
	r := newPoolResource(t, f)
	if _, err := r.put(context.Background(), "spc_1", "worker", nil); !client.IsConflict(err) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	n := 0
	for _, q := range f.reqs {
		if q.method == http.MethodPut {
			n++
		}
	}
	if n != rolePoolAttempts {
		t.Fatalf("PUTs = %d, want %d", n, rolePoolAttempts)
	}
}

func TestRolePoolPutDoesNotRetryOtherErrors(t *testing.T) {
	f := &poolAPI{putCodes: []int{http.StatusForbidden}}
	r := newPoolResource(t, f)
	if _, err := r.put(context.Background(), "spc_1", "worker", nil); err == nil {
		t.Fatal("want an error")
	}
	if len(f.reqs) != 2 {
		t.Fatalf("requests = %d, want GET + one PUT", len(f.reqs))
	}
}

func TestRolePoolNotFoundExplains(t *testing.T) {
	f := &poolAPI{putCodes: []int{http.StatusNotFound}}
	r := newPoolResource(t, f)
	_, err := r.put(context.Background(), "spc_1", "worker", nil)
	if !client.IsNotFound(err) || !strings.Contains(err.Error(), "needs SpaceMan 0.1.8 or later") {
		t.Fatalf("err = %v", err)
	}
}

func TestRolePoolDeleteSendsEmptyItems(t *testing.T) {
	f := &poolAPI{putCodes: []int{412, 200}}
	r := newPoolResource(t, f)
	if _, err := r.put(context.Background(), "spc_1", "worker", nil); err != nil {
		t.Fatal(err)
	}
	last := f.reqs[len(f.reqs)-1]
	if last.method != http.MethodPut || strings.TrimSpace(last.body) != `{"items":[]}` {
		t.Fatalf("delete request = %+v", last)
	}
}

func TestRolePoolOfEmptyItems(t *testing.T) {
	pools := []client.RolePool{{Role: "worker", Items: nil}, {Role: "qa", Items: []client.PoolEntry{{AccountID: "a"}}}}
	if _, ok := rolePoolOf(pools, "worker"); ok {
		t.Fatal("empty Items must mean no pool")
	}
	if es, ok := rolePoolOf(pools, "qa"); !ok || len(es) != 1 {
		t.Fatal("qa pool lost")
	}
	if _, ok := rolePoolOf(pools, "none"); ok {
		t.Fatal("unknown role has no pool")
	}
}

func TestPoolPriorityDefault(t *testing.T) {
	if poolPriority(0) != 100 || poolPriority(7) != 7 {
		t.Fatalf("poolPriority(0) = %d, (7) = %d", poolPriority(0), poolPriority(7))
	}
}

func TestBindingsCarryPriority(t *testing.T) {
	set := types.SetValueMust(bindingType, []attr.Value{types.ObjectValueMust(bindingType.AttrTypes, map[string]attr.Value{
		"account_id": types.StringValue("acc_1"), "default": types.BoolValue(true), "priority": types.Int64Value(7)})})
	var d diag.Diagnostics
	got := (&spaceAccountsResource{}).bindings(context.Background(), set, &d)
	if d.HasError() || len(got) != 1 || got[0].Priority != 7 || !got[0].Default || got[0].AccountID != "acc_1" {
		t.Fatalf("bindings = %+v, %v", got, d)
	}
}

func TestRoleNameValidation(t *testing.T) {
	for role, ok := range map[string]bool{"worker": true, "a": true, "qa-2": true, "Worker": false, "2fast": false, "": false, "a_b": false,
		strings.Repeat("a", 48): true, strings.Repeat("a", 49): false} {
		if roleNameRE.MatchString(role) != ok {
			t.Errorf("role %q: match = %v, want %v", role, !ok, ok)
		}
	}
}
