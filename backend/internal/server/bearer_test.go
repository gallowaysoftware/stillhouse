package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

func TestBearerUser(t *testing.T) {
	reached := false
	h := bearerUser(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, reached = rpc.CurrentUser(r.Context())
		reached = !reached
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/assistant/status", nil))
	if !reached {
		t.Error("a request with no token did not reach the handler")
	}
	reached = false
	r := httptest.NewRequest(http.MethodGet, "/api/assistant/status", nil)
	r.Header.Set("Authorization", "Bearer not-a-stillhouse-token")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if reached || w.Code != http.StatusUnauthorized {
		t.Errorf("a bad token answered %d and reached the handler: %v", w.Code, reached)
	}
}
