package session

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

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

// Session represents a user's session and contains all their data. This is
// marshalled into a []byte storage using json, so some restrictions such as
// only exported members are saved apply.
type Session struct {
	db      *sql.DB   // db handler
	id      uuid.UUID // session ID
	expires time.Time // time session should expire, only set on new sessions
	json    []byte    // json session from db, used to check if changes made

	GitHubID int // user's GitHub ID
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
		log.Println("session: unexpected error reading cookie:", err)
		return create(db, w), nil
	}

	// Get session from database
	var (
		jsonData []byte // json in db
	)

	id, err := uuid.Parse(cookie.Value)
	if err != nil {
		log.Printf("session: could not parse cookie %q", cookie.Value)
		return create(db, w), nil
	}

	// context ??
	err = db.QueryRow("SELECT json FROM sessions WHERE id=?", id[:]).Scan(&jsonData)
	switch {
	case err == sql.ErrNoRows:
		return create(db, w), nil
	case err != nil:
		return nil, errors.Wrap(err, fmt.Sprintf("session: could not get session id %q from db", cookie.Value))
	}

	var session Session
	if err := json.Unmarshal(jsonData, &session); err != nil {
		log.Printf("session: could not unmarshal session id %q (creating new one instead): %v", cookie.Value, err)
		return create(db, w), nil
	}
	session.db = db
	session.id = id
	session.json = jsonData
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
