package server

import "net/http"

// securityHeaders sets the response headers a browser needs in order to
// defend the app when something else has already gone wrong.
//
// None of these were present. The one that matters most here is
// frame-ancestors: without it the whole UI can be framed by another
// origin, and a clickjack overlay on /settings gets an owner to click
// "Delete distillery" — a cascading hard delete — or "Submit B266", which
// files a return. CSP is the second line of defence for any future stored
// XSS: operator-supplied notes flow into React, which escapes them today,
// but the export and print surfaces keep growing.
//
// The policy is deliberately strict because this app loads nothing from
// anywhere else: no CDN, no analytics, no external fonts. 'unsafe-inline'
// is needed for style-src only — Vite inlines a small style block and
// several components compute inline styles for chart geometry.
func securityHeaders(next http.Handler, dev bool) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		// HSTS only outside dev: asserting it over plain HTTP on a LAN
		// would pin a browser to a scheme the deployment may not serve.
		if !dev {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
