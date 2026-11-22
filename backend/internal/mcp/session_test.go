package mcp

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// A session id is not a credential: whoever presents it needs a live token
// of the person, in the role, that opened it.
func TestEveryMCPRequestIsAuthenticated(t *testing.T) {
	ctx := context.Background()
	admin := testdb.AdminPool(t)
	q := sqlcgen.New(admin)
	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name: "MCP " + uuid.NewString(), CraSpiritsLicenceNumber: "MCP-" + uuid.NewString(), DefaultJurisdiction: "CA-PE",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	token := func(role sqlcgen.UserRole) (string, uuid.UUID) {
		t.Helper()
		u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
			TenantID: tenant.ID, Email: "mcp-" + uuid.NewString() + "@example.com",
			PasswordHash: "x", DisplayName: "MCP Test", Role: role,
		})
		if err != nil {
			t.Fatal(err)
		}
		raw := rpc.APITokenPrefix + uuid.NewString()
		sum := sha256.Sum256([]byte(raw))
		if _, err := admin.Exec(ctx,
			"INSERT INTO api_tokens (token_hash, tenant_id, user_id, name) VALUES ($1, $2, $3, 'test')",
			sum[:], tenant.ID, u.ID); err != nil {
			t.Fatal(err)
		}
		return raw, u.ID
	}
	owner, ownerID := token(sqlcgen.UserRoleOwner)
	viewer, _ := token(sqlcgen.UserRoleViewer)

	srv := httptest.NewServer(NewHandler(Deps{Queries: sqlcgen.New(testdb.AppPool(t))}))
	defer srv.Close()

	post := func(bearer, session, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header.Get("Mcp-Session-Id")
	}
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	const list = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`

	if code, _ := post("", "", initialize); code != http.StatusUnauthorized {
		t.Errorf("opening a session with no token: %d, want 401", code)
	}
	code, session := post(owner, "", initialize)
	if code != http.StatusOK || session == "" {
		t.Fatalf("opening a session: %d, id %q", code, session)
	}
	for _, tc := range []struct {
		name, bearer string
		want         int
	}{
		{"its own token", owner, http.StatusOK},
		{"no token", "", http.StatusUnauthorized},
		{"a made-up token", rpc.APITokenPrefix + "nope", http.StatusUnauthorized},
		{"somebody else's token", viewer, http.StatusForbidden},
	} {
		if code, _ := post(tc.bearer, session, list); code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, code, tc.want)
		}
	}

	if _, err := admin.Exec(ctx, "UPDATE users SET role = 'viewer' WHERE id = $1", ownerID); err != nil {
		t.Fatal(err)
	}
	if code, _ := post(owner, session, list); code != http.StatusForbidden {
		t.Errorf("after the role changed: %d, want 403", code)
	}

	_, session = post(owner, "", initialize)
	if _, err := admin.Exec(ctx, "UPDATE api_tokens SET revoked_at = NOW() WHERE user_id = $1", ownerID); err != nil {
		t.Fatal(err)
	}
	if code, _ := post(owner, session, list); code != http.StatusUnauthorized {
		t.Errorf("after the token was revoked: %d, want 401", code)
	}
}
