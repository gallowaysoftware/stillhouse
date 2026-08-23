package rpc

import (
	"context"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// sessionAuthAtKey holds, in the session itself, the moment this session
// authenticated. Stored as an RFC3339 string rather than a time.Time so
// it round-trips through scs's gob encoding without needing a type
// registration, and so an old session missing the key is simply the empty
// string rather than a decode error.
const sessionAuthAtKey = "authenticated_at"

// StampSessionAuth records when the current session authenticated. Called
// wherever a session becomes authenticated — login, invite signup — and
// again by ChangeMyPassword, which revokes every session and then
// re-stamps its own so the person who just changed their password isn't
// the one thrown out.
func StampSessionAuth(sm *scs.SessionManager, ctx context.Context, at time.Time) {
	sm.Put(ctx, sessionAuthAtKey, at.UTC().Format(time.RFC3339Nano))
}

// SessionSurvivesRevocation reports whether a session belonging to u is
// still good.
//
// Sessions live inside scs's opaque blob, so there is no session table to
// delete from by user id. The mechanism instead is a watermark: writing a
// new password sets users.sessions_revoked_at, and any session that
// authenticated before that moment is dead. One UPDATE invalidates every
// session a user has anywhere, including on a device they no longer
// possess — which is the entire reason a person changes a password they
// think has leaked.
//
// Two edge cases, both resolved toward safety:
//
//   - No watermark (the common case) means nothing has ever been revoked,
//     so the session stands.
//   - A watermark but no stamp means a session that predates stage 154.
//     It is treated as older than any watermark and dies, which is
//     correct: it was issued under the password being replaced.
func SessionSurvivesRevocation(sm *scs.SessionManager, ctx context.Context, u sqlcgen.User) bool {
	if !u.SessionsRevokedAt.Valid {
		return true
	}
	raw := sm.GetString(ctx, sessionAuthAtKey)
	if raw == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return false
	}
	// Not-before rather than after: ChangeMyPassword re-stamps its own
	// session with exactly the watermark it just wrote, and that session
	// must survive.
	return !at.Before(u.SessionsRevokedAt.Time)
}
