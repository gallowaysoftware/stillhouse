// Package assistant is the brewing assistant: a chat turn run by a language
// model the operator hosts, with the MCP floor tools as the only thing it
// can do.
//
// The model is whatever answers at STILLHOUSE_ASSISTANT_URL — any server
// speaking the OpenAI chat-completions protocol. What the operator types
// and photographs, and what the tools return, goes there and nowhere else.
// Unset, the assistant is off.
//
// Every tool runs as the signed-in user through the MCP server, so role,
// row-level security and audit are the ones that already apply.
package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/mcp"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

const (
	// A phone photo scaled by the page is well under a megabyte; this is
	// room for several.
	maxBody    = 12 << 20
	maxHistory = 60
	maxPhotos  = 4
	// A turn that has not settled in this many rounds is going in circles.
	maxRounds   = 12
	turnTimeout = 4 * time.Minute
)

type Config struct {
	URL    string // base URL including /v1
	Model  string
	APIKey string // optional
}

func (c Config) Enabled() bool { return c.URL != "" && c.Model != "" }

// Message is one chat-completions message. The page holds the
// conversation and sends it back; nothing is stored here, because the
// record of the batch is the entries themselves.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type event struct {
	Message *Message `json:"message,omitempty"`
	Error   string   `json:"error,omitempty"`
	Done    bool     `json:"done,omitempty"`
}

type Handler struct {
	cfg    Config
	deps   mcp.Deps
	logger *slog.Logger
	client *http.Client

	mu   sync.Mutex
	busy map[uuid.UUID]bool
}

func New(cfg Config, deps mcp.Deps, logger *slog.Logger) *Handler {
	return &Handler{
		cfg: cfg, deps: deps, logger: logger, busy: map[uuid.UUID]bool{},
		client: &http.Client{
			// The endpoint is configured by the operator; a redirect would
			// carry the conversation somewhere they did not name.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Status says whether the assistant is configured, so the page can explain
// itself rather than fail.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if _, ok := rpc.CurrentUser(r.Context()); !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": h.cfg.Enabled(), "model": h.cfg.Model})
}

// Chat runs one turn, streaming each message as a line of JSON.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	user, ok := rpc.CurrentUser(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// A cross-site form cannot send this type without a preflight, which
	// nothing here answers.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "application/json only", http.StatusUnsupportedMediaType)
		return
	}
	if !h.cfg.Enabled() {
		http.Error(w, "the assistant is not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Messages []Message `json:"messages"`
		TimeZone string    `json:"time_zone"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		http.Error(w, "that message could not be read; photos may be too large", http.StatusBadRequest)
		return
	}
	history, err := prepare(req.Messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// One turn at a time: a double tap on a wet screen must not record twice.
	if !h.acquire(user.ID) {
		http.Error(w, "still working on the last message", http.StatusConflict)
		return
	}
	defer h.release(user.ID)

	ctx, cancel := context.WithTimeout(r.Context(), turnTimeout)
	defer cancel()
	out := http.NewResponseController(w)
	// The server's own write deadline is shorter than a turn can be.
	if err := out.SetWriteDeadline(time.Now().Add(turnTimeout + 10*time.Second)); err != nil {
		h.logger.Warn("assistant: write deadline", "err", err)
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	send := func(e event) {
		_ = enc.Encode(e)
		_ = out.Flush()
	}

	if err := h.turn(ctx, user, history, req.TimeZone, func(m Message) { send(event{Message: &m}) }); err != nil {
		h.logger.Error("assistant turn", "err", err)
		send(event{Error: userMessage(err)})
		return
	}
	send(event{Done: true})
}

func (h *Handler) acquire(id uuid.UUID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy[id] {
		return false
	}
	h.busy[id] = true
	return true
}

func (h *Handler) release(id uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.busy, id)
}

var (
	errModel  = errors.New("the model did not answer")
	errRounds = errors.New("gave up after too many steps")
)

func userMessage(err error) string {
	const check = " Anything shown above was recorded."
	switch {
	case errors.Is(err, errRounds):
		return "I went in circles and stopped." + check
	case errors.Is(err, context.DeadlineExceeded):
		return "That took too long and I stopped." + check
	case errors.Is(err, errModel):
		return "The model isn't answering." + check
	}
	return "Something went wrong." + check
}

func (h *Handler) turn(ctx context.Context, user sqlcgen.User, history []Message, tz string, emit func(Message)) error {
	session, err := mcp.ConnectFloor(ctx, h.deps, user)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	var tools []map[string]any
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return err
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.InputSchema,
		}})
	}

	messages := append([]Message{{Role: "system", Content: text(systemPrompt(user, tz, time.Now()))}}, history...)
	for range maxRounds {
		reply, err := h.complete(ctx, messages, tools)
		if err != nil {
			return err
		}
		messages = append(messages, reply)
		emit(reply)
		if len(reply.ToolCalls) == 0 {
			return nil
		}
		for _, call := range reply.ToolCalls {
			result := runTool(ctx, session, call)
			messages = append(messages, result)
			emit(result)
		}
	}
	return errRounds
}

func runTool(ctx context.Context, session *mcpsdk.ClientSession, call ToolCall) Message {
	fail := func(s string) Message {
		return Message{Role: "tool", ToolCallID: call.ID, Content: text(s), IsError: true}
	}
	args := map[string]any{}
	if strings.TrimSpace(call.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return fail("the arguments were not valid JSON; nothing was recorded")
		}
	}
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: call.Function.Name, Arguments: args})
	if err != nil {
		return fail(err.Error())
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return Message{Role: "tool", ToolCallID: call.ID, Content: text(sb.String()), IsError: res.IsError}
}

func (h *Handler) complete(ctx context.Context, messages []Message, tools []map[string]any) (Message, error) {
	wire := make([]Message, len(messages))
	copy(wire, messages)
	for i := range wire {
		wire[i].IsError = false
	}
	body, err := json.Marshal(map[string]any{
		"model": h.cfg.Model, "messages": wire, "tools": tools, "temperature": 0,
	})
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.cfg.URL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.APIKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %w", errModel, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Message{}, fmt.Errorf("%w: %w", errModel, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("%w: status %d: %.300s", errModel, resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return Message{}, fmt.Errorf("%w: unreadable reply: %.300s", errModel, raw)
	}
	m := out.Choices[0].Message
	m.Role = "assistant"
	if len(m.Content) == 0 || string(m.Content) == "null" {
		m.Content = text("")
	}
	for i := range m.ToolCalls {
		m.ToolCalls[i].Type = "function"
		if m.ToolCalls[i].ID == "" {
			m.ToolCalls[i].ID = "call_" + uuid.NewString()
		}
	}
	return m, nil
}

func systemPrompt(user sqlcgen.User, tz string, now time.Time) string {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	return fmt.Sprintf(`%s

You are talking to %s on a phone, in the middle of work. Answer in one or two short plain sentences, without markdown. It is now %s (%s).`,
		mcp.Instructions, user.DisplayName, now.In(loc).Format("Monday 2006-01-02 15:04 -07:00"), loc)
}

func text(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// prepare bounds what the page sent back and repairs a turn that was cut
// off, so a dropped connection does not leave a conversation the model
// server refuses.
func prepare(in []Message) ([]Message, error) {
	if len(in) == 0 || in[len(in)-1].Role != "user" {
		return nil, errors.New("the last message must be the operator's")
	}
	if len(in) > maxHistory {
		in = in[len(in)-maxHistory:]
	}
	// Start on the operator's words: a tool result without its call is refused.
	for len(in) > 0 && in[0].Role != "user" {
		in = in[1:]
	}
	var out []Message
	pending := map[string]bool{}
	var order []string
	settle := func() {
		for _, id := range order {
			if pending[id] {
				out = append(out, Message{Role: "tool", ToolCallID: id,
					Content: text("interrupted; it may or may not have been recorded. Read the run back before recording it again.")})
			}
		}
		clear(pending)
		order = nil
	}
	for i, m := range in {
		switch m.Role {
		case "user":
			settle()
			content, err := userContent(m.Content, i == len(in)-1)
			if err != nil {
				return nil, err
			}
			out = append(out, Message{Role: "user", Content: content})
		case "assistant":
			settle()
			if !isString(m.Content) {
				return nil, errors.New("unreadable conversation")
			}
			out = append(out, Message{Role: "assistant", Content: m.Content, ToolCalls: m.ToolCalls})
			for _, c := range m.ToolCalls {
				pending[c.ID] = true
				order = append(order, c.ID)
			}
		case "tool":
			if !pending[m.ToolCallID] || !isString(m.Content) {
				continue
			}
			delete(pending, m.ToolCallID)
			out = append(out, Message{Role: "tool", ToolCallID: m.ToolCallID, Content: m.Content})
		default:
			return nil, fmt.Errorf("unexpected role %q", m.Role)
		}
	}
	return out, nil
}

func isString(raw json.RawMessage) bool {
	var s string
	return len(raw) == 0 || json.Unmarshal(raw, &s) == nil
}

// userContent accepts words and photos. Photos are inline data only, so the
// model server is never asked to fetch a URL, and only the newest message
// keeps its photos.
func userContent(raw json.RawMessage, newest bool) (json.RawMessage, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return text(s), nil
	}
	var parts []part
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errors.New("unreadable message")
	}
	var kept []part
	photos := 0
	for _, p := range parts {
		switch {
		case p.Type == "text":
			kept = append(kept, part{Type: "text", Text: p.Text})
		case p.Type == "image_url" && p.ImageURL != nil:
			if !newest {
				kept = append(kept, part{Type: "text", Text: "(a photo, seen earlier)"})
				continue
			}
			if photos++; photos > maxPhotos {
				return nil, fmt.Errorf("at most %d photos at a time", maxPhotos)
			}
			if !isPhoto(p.ImageURL.URL) {
				return nil, errors.New("photos must be JPEG, PNG or WebP")
			}
			kept = append(kept, p)
		default:
			return nil, errors.New("unreadable message")
		}
	}
	if len(kept) == 0 {
		return nil, errors.New("an empty message")
	}
	return json.Marshal(kept)
}

func isPhoto(url string) bool {
	for _, t := range []string{"jpeg", "png", "webp"} {
		if strings.HasPrefix(url, "data:image/"+t+";base64,") {
			return true
		}
	}
	return false
}
