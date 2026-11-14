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
