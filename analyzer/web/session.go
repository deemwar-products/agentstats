package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Session is an HMAC-signed cookie `github_id|exp|hexmac`, signed with
// SESSION_KEY (a secret). No token or PII in the cookie. Ported from the
// retired Rust worker's session.rs (docs 05).

const (
	cookieName = "as_session"
	maxAgeSecs = 60 * 60 * 24 * 30 // 30 days
)

// issueSession signs `github_id|exp` and returns the full cookie value.
func issueSession(githubID int64, key []byte, now int64) string {
	exp := now + maxAgeSecs
	payload := strconv.FormatInt(githubID, 10) + "|" + strconv.FormatInt(exp, 10)
	return payload + "|" + sign(payload, key)
}

// verifySession returns the github_id if the signature is valid and unexpired.
func verifySession(value string, key []byte, now int64) (int64, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return 0, false
	}
	payload := parts[0] + "|" + parts[1]
	expected, err := hex.DecodeString(parts[2])
	if err != nil {
		return 0, false
	}
	mac := hmacBytes(payload, key)
	if !hmac.Equal(mac, expected) {
		return 0, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || exp < now {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func sign(payload string, key []byte) string {
	return hex.EncodeToString(hmacBytes(payload, key))
}

func hmacBytes(payload string, key []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// setSessionCookie writes the signed session cookie (HttpOnly, Secure, Lax).
func setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAgeSecs,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 0,
	})
}

// currentUserID reads and verifies the session cookie.
func (s *Server) currentUserID(r *http.Request) (int64, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return 0, false
	}
	return verifySession(c.Value, s.sessionKey, time.Now().Unix())
}
