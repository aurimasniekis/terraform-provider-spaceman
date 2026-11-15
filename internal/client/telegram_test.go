package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramBotIfMatchAndBody(t *testing.T) {
	var gotMatch []string
	var gotBody []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMatch = append(gotMatch, r.Header.Get("If-Match"))
		if r.Method == http.MethodPut {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			gotBody = append(gotBody, b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"enabled":false,"mode":"webhook","has_token":true,"token_version":1,"version":1}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.PutTelegramBot(ctx, 0, TelegramBotInput{BotToken: "1:x", WebhookIPAllowlist: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutTelegramBot(ctx, 1, TelegramBotInput{WebhookIPAllowlist: []string{"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteTelegramBot(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if gotMatch[0] != "" || gotMatch[1] == "" || gotMatch[2] == "" {
		t.Fatalf("If-Match = %q", gotMatch)
	}
	if _, ok := gotBody[1]["bot_token"]; ok {
		t.Fatal("an empty token must be omitted (keeps the stored one)")
	}
	if _, ok := gotBody[0]["webhook_ip_allowlist"]; !ok {
		t.Fatal("the allowlist must always be sent so an empty set clears it")
	}
}

func TestTelegramSettingsPathsAndBodies(t *testing.T) {
	type call struct{ method, path, match, body string }
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		raw, _ := json.Marshal(b)
		calls = append(calls, call{r.Method, r.URL.Path, r.Header.Get("If-Match"), string(raw)})
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stored":true,"version":1,"send_limits":{"global_per_second":12.5}}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	n := int64(30)
	s, err := c.PutTelegramSettings(ctx, 0, TelegramSettingsInput{LinkReconfirmDays: &n})
	if err != nil || s.SendLimits.GlobalPerSecond != 12.5 {
		t.Fatalf("%v %+v", err, s)
	}
	yes := true
	if _, err := c.PutSpaceTelegram(ctx, "spc_1", 2, SpaceTelegramInput{Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteTelegramSettings(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSpaceTelegram(ctx, "spc_1", 3); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{"PUT", "/api/v1/telegram/settings", "", `{"link_reconfirm_days":30}`},
		{"PUT", "/api/v1/spaces/spc_1/telegram", "2", `{"enabled":true}`},
		{"DELETE", "/api/v1/telegram/settings", "1", `null`},
		{"DELETE", "/api/v1/spaces/spc_1/telegram", "3", `null`},
	}
	for i, w := range want {
		g := calls[i]
		if g.method != w.method || g.path != w.path || g.body != w.body || (w.match == "") != (g.match == "") {
			t.Errorf("call %d = %+v, want %+v", i, g, w)
		}
	}
}
