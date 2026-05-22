package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// loggingMiddleware logs every HTTP request after it completes. Captures
// method, path, status code, response bytes, duration, and (for Connect
// calls) the RPC procedure. Health checks are dropped to keep logs quiet.
func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"bytes", ww.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		// Connect RPC paths have the shape /package.Service/Method.
		if strings.HasPrefix(r.URL.Path, "/stillhouse.") {
			attrs = append(attrs, "kind", "rpc")
		}
		if ww.status >= 500 {
			logger.Error("request", attrs...)
		} else {
			logger.Info("request", attrs...)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.wrote = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Flush forwards to the underlying ResponseWriter if it supports Flush.
// Connect streaming responses need this.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
