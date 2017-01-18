package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

const (
	cookieName            = "sid"
	cookiePath            = "/"
	cookieDurationSeconds = 90 * 86400 // 90 days
	cookieSecure          = true
	cookieHTTPOnly        = true
)

// CtxKey is the key to use when storing session in a Context
type CtxKey struct{}

// Session represents a user's session and contains all their data. This is
// marshalled into a []byte storage using json, so some restrictions such as
// only exported members are saved apply.
type Session struct {
	logger  *logrus.Entry // structured logger
	db      *sql.DB       // db handler
	id      uuid.UUID     // session ID
	expires time.Time     // time session should expire, only set on new sessions
	json    []byte        // json session from db, used to check if changes made

	UserID           int       // Our User ID
	GitHubID         int       // User's GitHub ID
	GitHubOAuthState uuid.UUID // State/CSRF token when using GitHub OAuth flow
}

// GetOrCreate reads the http.Request looking for a session ID and attempts to
// load this sesstion from the database. Most errors are handled by creating a
// new session. Call Save() on the session to persist to db.
func GetOrCreate(db *sql.DB, w http.ResponseWriter, r *http.Request) (*Session, error) {
	// Get session id from cookie
	cookie, err := r.Cookie(cookieName)
	switch {
	case err == http.ErrNoCookie:
		return create(db, w), nil
	case err != nil:
		return create(db, w), nil
	}

	// Get session from database
	var (
		jsonData []byte // json in db
		expires  time.Time
	)

	id, err := uuid.Parse(cookie.Value)
	if err != nil {
		return create(db, w), nil
	}

	// context ??
	err = db.QueryRow("SELECT json, expires_at FROM sessions WHERE id=?", id[:]).Scan(&jsonData, &expires)
	switch {
	case err == sql.ErrNoRows:
		return create(db, w), nil
	case err != nil:
		return nil, errors.Wrap(err, fmt.Sprintf("session: could not get session id %q from db", cookie.Value))
	}

	var session Session
	if err := json.Unmarshal(jsonData, &session); err != nil {
		return create(db, w), nil
	}
	session.db = db
	session.id = id
	session.json = jsonData
	session.expires = expires
	return &session, nil
}

// create creates a new session and adds a cookie to responseWriter. The
// session is not written to db.
func create(db *sql.DB, w http.ResponseWriter) *Session {
	s := &Session{
		db:      db,
		id:      uuid.New(),
		expires: time.Now().Add(cookieDurationSeconds * time.Second),
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.id.String(),
		Path:     cookiePath,
		Expires:  s.expires,
		Secure:   cookieSecure,
		HttpOnly: cookieHTTPOnly,
	})
	return s
}

// Save saves the session to the database.
func (s *Session) Save() error {
	jsonData, err := json.Marshal(s)
	if err != nil {
		return errors.Wrap(err, "session: could not marshal session to json")
	}

	if bytes.Equal(s.json, jsonData) {
		// No changes to session, don't write it
		return nil
	}

	query := `INSERT INTO sessions (id, json, expires_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE json = ?`
	_, err = s.db.Exec(query, s.id[:], jsonData, s.expires, jsonData)
	if err != nil {
		return errors.Wrap(err, "session: could not save to db")
	}
	return nil
}

// FromContext returns the session from a context.
func FromContext(ctx context.Context) *Session {
	return ctx.Value(CtxKey{}).(*Session)
}

// LoggedIn checks if the user is currently logged in.
func (s *Session) LoggedIn() bool {
	return s.UserID != 0
}

// Delete deletes the user's sessions from the database and sets the cookie to expire.
func (s *Session) Delete(w http.ResponseWriter) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", s.id)
	if err != nil {
		return errors.Wrap(err, "session: could not delete session from db")
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: cookiePath, MaxAge: -1})
	return nil
}
