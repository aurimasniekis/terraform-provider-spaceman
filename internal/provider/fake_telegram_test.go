package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// fakeForumChatID is the one chat the fake Telegram knows: a forum supergroup the bot is a member of.
const fakeForumChatID int64 = -1001234567890

// startFakeTelegram serves a minimal Telegram Bot API (<base>/bot<token>/<method>) for the acceptance tests. getMe
// answers with a bot whose ID is the token's numeric prefix, getUpdates is an empty long poll, getChat finds only
// fakeForumChatID, and every other method succeeds. It never needs the real network.
func startFakeTelegram() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "bot") {
			http.NotFound(w, r)
			return
		}
		token, method := strings.TrimPrefix(parts[0], "bot"), parts[1]
		id, _, _ := strings.Cut(token, ":")
		var result any = true
		switch method {
		case "getMe":
			n := int64(0)
			for _, c := range id {
				n = n*10 + int64(c-'0')
			}
			result = map[string]any{"id": n, "is_bot": true, "first_name": "Test", "username": "tf_bot_" + id}
		case "getChat":
			// Only the forum supergroup fakeForumChatID is one the bot is in; Telegram answers 400 for any other chat.
			var p struct {
				ChatID int64 `json:"chat_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.ChatID != fakeForumChatID {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: chat not found"})
				return
			}
			result = map[string]any{"id": p.ChatID, "type": "supergroup", "title": "Forum", "is_forum": true}
		case "getWebhookInfo":
			result = map[string]any{"url": "", "has_custom_certificate": false, "pending_update_count": 0}
		case "getUpdates":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			result = []any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
}
