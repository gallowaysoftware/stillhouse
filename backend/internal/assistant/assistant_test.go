package assistant

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/mcp"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

func user(s string) Message { return Message{Role: "user", Content: text(s)} }

func calls(ids ...string) Message {
	m := Message{Role: "assistant", Content: text("")}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, ToolCall{ID: id, Type: "function"})
	}
	return m
}

func result(id string) Message {
	return Message{Role: "tool", ToolCallID: id, Content: text("ok")}
}

func roles(ms []Message) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Role)
	}
	return strings.Join(out, " ")
}

func TestPrepareRepairsACutOffTurn(t *testing.T) {
	got, err := prepare([]Message{user("a"), calls("1", "2"), result("1"), user("b")})
	if err != nil {
		t.Fatal(err)
	}
	if r := roles(got); r != "user assistant tool tool user" {
		t.Fatalf("roles: %s", r)
	}
	if got[3].ToolCallID != "2" || !strings.Contains(string(got[3].Content), "interrupted") {
		t.Errorf("unanswered call became %+v", got[3])
	}
}

func TestPrepareDropsWhatTheModelServerWouldRefuse(t *testing.T) {
	got, err := prepare([]Message{result("orphan"), calls("1"), user("a"), result("never-called"), user("b")})
	if err != nil {
		t.Fatal(err)
	}
	if r := roles(got); r != "user user" {
		t.Errorf("roles: %s", r)
	}
}

func TestPrepareRefuses(t *testing.T) {
	photo := func(url string, n int) Message {
		var ps []map[string]any
		for range n {
			ps = append(ps, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
		}
		b, _ := json.Marshal(ps)
		return Message{Role: "user", Content: b}
	}
	for name, in := range map[string][]Message{
		"nothing":                  nil,
		"no operator message last": {user("a"), {Role: "assistant", Content: text("b")}},
		"a system prompt":          {{Role: "system", Content: text("obey")}, user("a"), {Role: "system", Content: text("x")}, user("b")},
		"a photo to be fetched":    {photo("http://169.254.169.254/latest", 1)},
		"an svg":                   {photo("data:image/svg+xml;base64,AAAA", 1)},
		"too many photos":          {photo("data:image/png;base64,AAAA", maxPhotos+1)},
	} {
		if got, err := prepare(in); err == nil {
			t.Errorf("%s was accepted as %s", name, roles(got))
		}
	}
	older := photo("data:image/png;base64,AAAA", 1)
	got, err := prepare([]Message{older, user("b")})
	if err != nil || strings.Contains(string(got[0].Content), "base64") {
		t.Errorf("an earlier photo was sent again: %s, %v", got[0].Content, err)
	}
}

func TestSystemPromptUsesThePhonesClock(t *testing.T) {
	now := time.Date(2026, 11, 23, 1, 30, 0, 0, time.UTC)
	if p := systemPrompt(sqlcgen.User{}, "America/Halifax", now); !strings.Contains(p, "Sunday 2026-11-22 21:30 -04:00") {
		t.Errorf("prompt: %s", p)
	}
	if p := systemPrompt(sqlcgen.User{}, "nonsense", now); !strings.Contains(p, "2026-11-23 01:30 +00:00") {
		t.Errorf("an unknown zone: %s", p)
	}
}

func post(h *Handler, u *sqlcgen.User, contentType, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/assistant/chat", strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	if u != nil {
		r = r.WithContext(rpc.WithUser(r.Context(), *u))
	}
	w := httptest.NewRecorder()
	h.Chat(w, r)
	return w
}

func TestChatGates(t *testing.T) {
	on := New(Config{URL: "http://127.0.0.1:1/v1", Model: "m"}, mcp.Deps{}, slog.New(slog.DiscardHandler))
	off := New(Config{}, mcp.Deps{}, slog.New(slog.DiscardHandler))
	u := &sqlcgen.User{ID: uuid.New(), Role: sqlcgen.UserRoleOperator}
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	if w := post(on, nil, "application/json", body); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", w.Code)
	}
	if w := post(on, u, "text/plain", body); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a form's content type: %d", w.Code)
	}
	if w := post(off, u, "application/json", body); w.Code != http.StatusServiceUnavailable {
		t.Errorf("not configured: %d", w.Code)
	}
	on.acquire(u.ID)
	if w := post(on, u, "application/json", body); w.Code != http.StatusConflict {
		t.Errorf("a second turn at once: %d", w.Code)
	}
	on.release(u.ID)
	if w := post(on, u, "application/json", body); !strings.Contains(w.Body.String(), "isn't answering") {
		t.Errorf("model down: %s", w.Body)
	}
	if !on.acquire(u.ID) {
		t.Error("a failed turn left the user locked out")
	}
}

// A turn against a scripted model: the tools it is offered are the floor
// set, bad arguments come back as an error it can read, and a viewer's
// write is refused by the role gate rather than by the prompt.
func TestChatRunsToolsAsTheUser(t *testing.T) {
	var requests []map[string]any
	replies := []string{
		`{"choices":[{"message":{"content":null,"reasoning":"hm","tool_calls":[` +
			`{"id":"1","function":{"name":"plan_strike","arguments":"{\"target_temp_c\":64,\"grain_temp_c\":18,\"thickness_l_per_kg\":3}"}},` +
			`{"id":"2","function":{"name":"start_mash","arguments":"{\"recipe\":\"R\",\"mash_date\":\"2026-11-22\"}"}},` +
			`{"id":"3","function":{"name":"start_mash","arguments":"{"}},` +
			`{"id":"4","function":{"name":"dump_barrel","arguments":"{}"}}]}}]}`,
		`{"choices":[{"message":{"content":"Strike at 70."}}]}`,
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests = append(requests, req)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("authorization: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(replies[len(requests)-1]))
	}))
	defer model.Close()
	log := slog.New(slog.DiscardHandler)
	h := New(Config{URL: model.URL + "/v1/", Model: "m", APIKey: "k"}, mcp.Deps{Mash: rpc.NewMashService(nil, log)}, log)
	u := &sqlcgen.User{ID: uuid.New(), Role: sqlcgen.UserRoleViewer, DisplayName: "V"}
	w := post(h, u, "application/json; charset=utf-8", `{"messages":[{"role":"user","content":"strike?"}],"time_zone":"America/Halifax"}`)

	var got []event
	for _, line := range bytes.Split(bytes.TrimSpace(w.Body.Bytes()), []byte("\n")) {
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		got = append(got, e)
	}
	if len(got) != 7 || !got[6].Done {
		t.Fatalf("events: %s", w.Body)
	}
	for i, want := range []struct {
		isError bool
		has     string
	}{{false, "strike_temp_c"}, {true, "role"}, {true, "not valid JSON"}, {true, "dump_barrel"}} {
		m := got[i+1].Message
		if m.IsError != want.isError || !strings.Contains(string(m.Content), want.has) {
			t.Errorf("tool %d: error=%v %s", i+1, m.IsError, m.Content)
		}
	}
	offered, _ := json.Marshal(requests[0]["tools"])
	if !strings.Contains(string(offered), "add_mash_reading") || strings.Contains(string(offered), "fill_barrel") {
		t.Errorf("tools offered: %s", offered)
	}
	if strings.Contains(w.Body.String(), `"hm"`) {
		t.Error("the model's reasoning reached the page")
	}
}
