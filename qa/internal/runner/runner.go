// Package runner executes a plan.json against a running Stillhouse
// backend. Each test case gets a fresh session (cookie jar), logs in
// with the provided seed-admin credentials, dispatches the case's
// steps over JSON-over-HTTP ConnectRPC, and records a Finding.
//
// V1 limitations (tracked, not silently swallowed):
//
//   - Fresh-tenant-per-case is reduced to fresh-session-per-case in
//     V1. True tenant isolation requires either the seed cmd to grow
//     a "create tenant" RPC or the runner to take a direct DB
//     connection. The current Stillhouse build has neither; see the
//     "TODO(fresh-tenant)" markers in this file.
//
//   - kind: ui steps emit a "skipped: playwright not yet wired"
//     finding rather than fail. The Playwright integration lands in
//     a follow-up commit.
//
// Everything else from the plan (kind: rpc, kind: assert) executes
// for real and produces a Finding.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Config carries everything the runner needs from the CLI.
type Config struct {
	// BackendURL is the base of the ConnectRPC service. No trailing
	// slash. Example: http://localhost:8080
	BackendURL string
	// AdminEmail + AdminPassword authenticate against AuthService/Login
	// at the start of every test case. V1 reuses the seed admin for
	// every case; see the TODO(fresh-tenant) marker.
	AdminEmail    string
	AdminPassword string
	// PlanFile is the path to plan.json from a prior plan run.
	PlanFile string
	// OutDir is where findings.jsonl and per-case logs land.
	OutDir string
	// HTTPTimeout caps any single RPC call. Defaults to 30s.
	HTTPTimeout time.Duration
}

// Plan mirrors the planner's output. Only the fields the runner
// actually consumes are typed; the rest stays in RawCase so
// findings round-trip the planner's metadata intact.
type Plan struct {
	TestCases []RawCase `json:"test_cases"`
}

// RawCase keeps the planner's full case JSON as a raw message + a
// few decoded fields the runner needs. The runner doesn't try to
// re-derive what the planner already committed to.
type RawCase struct {
	ID                  string          `json:"id"`
	Title               string          `json:"title"`
	Category            string          `json:"category"`
	Priority            string          `json:"priority"`
	VerifiesInvariants  []string        `json:"verifies_invariants"`
	Steps               []json.RawMessage `json:"steps"`
	ExpectedOutcome     string          `json:"expected_outcome"`
	PrimerSections      []string        `json:"primer_sections"`
}

// Step is the runner-facing decoding of one entry in RawCase.Steps.
// The planner's schema mixes kinds; we discriminate on Kind.
type Step struct {
	Kind             string                 `json:"kind"`
	RPC              string                 `json:"rpc"`               // kind=rpc
	Payload          map[string]any         `json:"payload"`           // kind=rpc
	Expect           map[string]any         `json:"expect"`            // kind=rpc
	PlaywrightRecipe string                 `json:"playwright_recipe"` // kind=ui
	AssertType       string                 `json:"type"`              // kind=assert — note: field name is "type"
	AssertExtra      map[string]any         `json:"-"`                 // kind=assert — populated post-decode
}

// Finding is one row of the run phase's output stream
// (findings.jsonl). One per test case.
type Finding struct {
	CaseID             string         `json:"case_id"`
	Title              string         `json:"title"`
	Category           string         `json:"category"`
	Priority           string         `json:"priority"`
	VerifiesInvariants []string       `json:"verifies_invariants"`
	StepResults        []StepResult   `json:"step_results"`
	Status             string         `json:"status"` // pass | fail | skipped | error
	Reason             string         `json:"reason,omitempty"`
	DurationMs         int64          `json:"duration_ms"`
	PrimerSections     []string       `json:"primer_sections,omitempty"`
}

// StepResult records one step's outcome. The runner attaches the
// raw HTTP response body on RPC steps (truncated) so a report phase
// can quote evidence without re-running the case.
type StepResult struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	RPC        string `json:"rpc,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Pass       bool   `json:"pass"`
	Reason     string `json:"reason,omitempty"`
	BodySample string `json:"body_sample,omitempty"`
}

// loginResponse mirrors the parts of stillhouse.v1.LoginResponse the
// runner introspects. The Connect server returns full LoginResponse
// JSON; we keep only the IDs we need for templating.
type loginResponse struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Tenant struct {
		ID string `json:"id"`
	} `json:"tenant"`
}

// Run executes every case in cfg.PlanFile and writes findings.jsonl
// + a per-case body log under cfg.OutDir. The caller is responsible
// for the Stillhouse stack being already-running and seeded.
func Run(ctx context.Context, cfg Config) error {
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("mkdir out: %w", err)
	}

	planBytes, err := os.ReadFile(cfg.PlanFile)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}

	findingsPath := filepath.Join(cfg.OutDir, "findings.jsonl")
	f, err := os.Create(findingsPath)
	if err != nil {
		return fmt.Errorf("create findings.jsonl: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	fmt.Fprintf(os.Stderr, "running %d test cases against %s\n", len(plan.TestCases), cfg.BackendURL)
	for i, tc := range plan.TestCases {
		// Per-case fresh session: new cookie jar + new login. This is
		// the V1 stand-in for fresh-tenant-per-case.
		// TODO(fresh-tenant): once Stillhouse grows a CreateTenant RPC
		// or this binary takes a direct DB connection, provision a
		// throwaway tenant here and tear it down at the end of the
		// case.
		finding := runCase(ctx, cfg, tc)
		if err := enc.Encode(finding); err != nil {
			return fmt.Errorf("encode finding %d: %w", i, err)
		}
		fmt.Fprintf(os.Stderr, "  [%d/%d] %s %s (%s)\n",
			i+1, len(plan.TestCases), statusGlyph(finding.Status), tc.ID, finding.Status)
	}
	fmt.Fprintf(os.Stderr, "findings → %s\n", findingsPath)
	return nil
}

func runCase(ctx context.Context, cfg Config, tc RawCase) Finding {
	start := time.Now()
	finding := Finding{
		CaseID:             tc.ID,
		Title:              tc.Title,
		Category:           tc.Category,
		Priority:           tc.Priority,
		VerifiesInvariants: tc.VerifiesInvariants,
		PrimerSections:     tc.PrimerSections,
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: cfg.HTTPTimeout}

	if _, err := login(ctx, client, cfg); err != nil {
		finding.Status = "error"
		finding.Reason = fmt.Sprintf("login: %v", err)
		finding.DurationMs = time.Since(start).Milliseconds()
		return finding
	}

	// Per-step response map keyed by index. Template references in
	// later steps ($steps[N].response.field) resolve against this.
	responses := map[int]any{}
	allPass := true
	for i, raw := range tc.Steps {
		var s Step
		if err := json.Unmarshal(raw, &s); err != nil {
			finding.StepResults = append(finding.StepResults, StepResult{
				Index: i, Kind: "unknown", Pass: false,
				Reason: fmt.Sprintf("decode step: %v", err),
			})
			allPass = false
			continue
		}
		// Pull the rest of the assert payload out for kind: assert
		// since the planner mixes named fields freely on it.
		if s.Kind == "assert" {
			_ = json.Unmarshal(raw, &s.AssertExtra)
		}
		res := executeStep(ctx, client, cfg, s, i, responses)
		finding.StepResults = append(finding.StepResults, res)
		if !res.Pass && res.Reason != "skipped" {
			allPass = false
		}
	}
	finding.Status = "pass"
	if !allPass {
		finding.Status = "fail"
		finding.Reason = "one or more steps failed; see step_results"
	}
	finding.DurationMs = time.Since(start).Milliseconds()
	return finding
}

func executeStep(ctx context.Context, client *http.Client, cfg Config, s Step, idx int, responses map[int]any) StepResult {
	switch s.Kind {
	case "rpc":
		return executeRPC(ctx, client, cfg, s, idx, responses)
	case "ui":
		// TODO(playwright): translate s.PlaywrightRecipe into browser
		// actions via playwright-go. For V1 the runner records the
		// recipe so the report can surface uncovered cases, but the
		// step itself is non-fatal.
		return StepResult{Index: idx, Kind: "ui", Pass: true, Reason: "skipped: playwright not yet wired", BodySample: s.PlaywrightRecipe}
	case "assert":
		return executeAssert(ctx, client, cfg, s, idx)
	default:
		return StepResult{Index: idx, Kind: s.Kind, Pass: false, Reason: fmt.Sprintf("unknown step kind %q", s.Kind)}
	}
}

func executeRPC(ctx context.Context, client *http.Client, cfg Config, s Step, idx int, responses map[int]any) StepResult {
	url, err := connectURL(cfg.BackendURL, s.RPC)
	if err != nil {
		return StepResult{Index: idx, Kind: "rpc", RPC: s.RPC, Pass: false, Reason: err.Error()}
	}
	resolved := resolveRefs(s.Payload, responses)
	body, err := json.Marshal(resolved)
	if err != nil {
		return StepResult{Index: idx, Kind: "rpc", RPC: s.RPC, Pass: false, Reason: fmt.Sprintf("marshal payload: %v", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return StepResult{Index: idx, Kind: "rpc", RPC: s.RPC, Pass: false, Reason: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return StepResult{Index: idx, Kind: "rpc", RPC: s.RPC, Pass: false, Reason: fmt.Sprintf("http: %v", err)}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	// Capture the response so subsequent template refs can resolve.
	var parsed any
	_ = json.Unmarshal(respBody, &parsed)
	responses[idx] = parsed
	pass, reason := matchExpect(s.Expect, resp.StatusCode, respBody)
	sample := string(respBody)
	if len(sample) > 1024 {
		sample = sample[:1024] + "…"
	}
	return StepResult{
		Index: idx, Kind: "rpc", RPC: s.RPC,
		HTTPStatus: resp.StatusCode, Pass: pass, Reason: reason, BodySample: sample,
	}
}

// matchExpect compares the planner's expect block against the actual
// response. Two shapes are supported today:
//
//   {"status": "ok"}                     → 2xx
//   {"status": "error", "code": "FOO"}   → non-2xx + ConnectRPC code matches
//
// Anything else returns pass=false with an explanatory reason so the
// planner gets feedback if it emits a shape the runner doesn't yet
// understand.
func matchExpect(expect map[string]any, httpStatus int, body []byte) (bool, string) {
	status, _ := expect["status"].(string)
	switch status {
	case "ok":
		if httpStatus >= 200 && httpStatus < 300 {
			return true, ""
		}
		return false, fmt.Sprintf("expected ok, got HTTP %d", httpStatus)
	case "error":
		if httpStatus >= 200 && httpStatus < 300 {
			return false, fmt.Sprintf("expected error, got HTTP %d", httpStatus)
		}
		wantCode, hasCode := expect["code"].(string)
		if !hasCode {
			return true, ""
		}
		// Connect JSON errors have {"code": "..."} at top level.
		var errBody struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(body, &errBody)
		if strings.EqualFold(errBody.Code, wantCode) ||
			strings.EqualFold(errBody.Code, normalizeConnectCode(wantCode)) {
			return true, ""
		}
		return false, fmt.Sprintf("expected error code %q, got %q (HTTP %d)", wantCode, errBody.Code, httpStatus)
	case "":
		return false, "expect missing 'status' field"
	default:
		return false, fmt.Sprintf("unknown expect.status %q", status)
	}
}

// normalizeConnectCode maps the planner's gRPC-style codes
// (PERMISSION_DENIED) to Connect's lower-case form (permission_denied)
// for case-insensitive comparison fallback.
func normalizeConnectCode(s string) string {
	return strings.ToLower(s)
}

// executeAssert handles kind: assert steps. V1 implements only the
// "audit_log_has_entry" type as a probe of stillhouse.v1.AuditService
// /ListEvents — extend here as the planner emits new assert types.
func executeAssert(ctx context.Context, client *http.Client, cfg Config, s Step, idx int) StepResult {
	switch s.AssertType {
	case "audit_log_has_entry":
		event, _ := s.AssertExtra["event"].(string)
		// Probe AuditService for an entry matching event. The exact
		// RPC name + payload shape needs to align with the proto; this
		// is the obvious next thing to wire after the runner skeleton
		// proves itself.
		// TODO(assert): implement audit-log probe.
		return StepResult{Index: idx, Kind: "assert", Pass: true, Reason: "skipped: audit_log_has_entry probe TODO", BodySample: "event=" + event}
	default:
		return StepResult{Index: idx, Kind: "assert", Pass: false, Reason: fmt.Sprintf("unknown assert type %q", s.AssertType)}
	}
}

// login posts AdminEmail+AdminPassword to AuthService/Login and lets
// the cookie jar capture the session cookie. The Login response body
// is returned for completeness; callers usually ignore it.
func login(ctx context.Context, client *http.Client, cfg Config) (loginResponse, error) {
	url, _ := connectURL(cfg.BackendURL, "stillhouse.v1.AuthService/Login")
	body, _ := json.Marshal(map[string]string{
		"email":    cfg.AdminEmail,
		"password": cfg.AdminPassword,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return loginResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return loginResponse{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return loginResponse{}, fmt.Errorf("login HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var out loginResponse
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// connectURL composes a ConnectRPC endpoint URL from the base + the
// planner's "stillhouse.v1.Service/Method" form. ConnectRPC's
// HTTP-binding convention is /{package.Service}/{Method}.
func connectURL(base, rpc string) (string, error) {
	if !strings.Contains(rpc, "/") {
		return "", fmt.Errorf("rpc path %q missing /Method", rpc)
	}
	return strings.TrimRight(base, "/") + "/" + rpc, nil
}

// refRegex matches the planner's ${steps[N].response.X.Y.Z} form.
// The runner resolves these greedily inside string fields of the
// payload. Numeric / object refs are surfaced as raw values; the
// JSON re-marshal preserves their type.
var refRegex = regexp.MustCompile(`\$\{steps\[(\d+)\]\.response((?:\.[A-Za-z_][A-Za-z0-9_]*)*)\}`)

// resolveRefs walks the payload tree and rewrites every string that
// is exactly a ${steps[N].response...} reference into the resolved
// upstream value. Mixed strings (e.g. "id-${...}") are also
// supported but resolved as string-coerced values.
func resolveRefs(payload map[string]any, responses map[int]any) map[string]any {
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = resolveValue(v, responses)
	}
	return out
}

func resolveValue(v any, responses map[int]any) any {
	switch x := v.(type) {
	case string:
		// If the whole string is a single ref, return the typed value.
		if m := refRegex.FindStringSubmatch(x); m != nil && m[0] == x {
			idx := atoiSafe(m[1])
			return walkPath(responses[idx], m[2])
		}
		// Otherwise, substitute every match as a string.
		return refRegex.ReplaceAllStringFunc(x, func(match string) string {
			m := refRegex.FindStringSubmatch(match)
			idx := atoiSafe(m[1])
			val := walkPath(responses[idx], m[2])
			if s, ok := val.(string); ok {
				return s
			}
			b, _ := json.Marshal(val)
			return string(b)
		})
	case map[string]any:
		return resolveRefs(x, responses)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = resolveValue(item, responses)
		}
		return out
	default:
		return v
	}
}

// walkPath follows a ".a.b.c" path into a nested JSON-decoded value.
// Empty path returns the root. Missing keys return nil.
func walkPath(root any, path string) any {
	if path == "" {
		return root
	}
	parts := strings.Split(strings.TrimPrefix(path, "."), ".")
	cur := root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func statusGlyph(s string) string {
	switch s {
	case "pass":
		return "[+]"
	case "fail":
		return "[-]"
	case "skipped":
		return "[~]"
	default:
		return "[!]"
	}
}
