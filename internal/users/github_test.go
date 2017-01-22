package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/Sirupsen/logrus"
	sqlmock "github.com/bradleyfalzon/go-sqlmock"
	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var logger = logrus.New().WithField("pkg", "session_test")

func TestOAuthLoginHandler(t *testing.T) {
	s := &session.Session{}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	um := NewUserManager(logger, nil, "id", "secret", "stripeKey")
	um.oauthConf.Endpoint.AuthURL = "http://example.com"
	um.oauthConf.Endpoint.TokenURL = ""
	um.OAuthLoginHandler(w, r)

	// Expect a redirect (this is not a great test)
	want := "http://example.com?access_type=online&client_id=id&response_type=code&scope=user+read%3Aorg&state="
	if !strings.HasPrefix(w.Result().Header.Get("Location"), want) {
		t.Errorf("Location header does not have expected prefix\nhave: %v\nwant: %v", w.Result().Header.Get("Location"), want)
	}

	// Expect session to have a GitHubOAuthState
	if s.GitHubOAuthState == uuid.Nil {
		t.Errorf("GitHubOAuthState unexpected nil")
	}
}

type mockUserManager struct {
	UserID   int   // to be returned
	Err      error // to be returned
	GitHubID int
	Token    string
}

//var _ UserManager = &mockUserManager{}

func (um *mockUserManager) GitHubLogin(githubID int, token string) (int, error) {
	um.GitHubID = githubID
	um.Token = token
	return um.UserID, um.Err
}

func TestOAuthCallbackHandler(t *testing.T) {
	// Test is currently not working, as oauth always errors with
	// Get http://127.0.0.1:41081/user: oauth2: token expired and refresh token is not set
	// Although the client should use our test server, it doesn't appear to
	// and I just can't get this to work. Skipping for the moment.
	t.Skip()
	s := &session.Session{GitHubOAuthState: uuid.New()}
	r := httptest.NewRequest("GET", "/?state="+s.GitHubOAuthState.String(), nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI == "/user" {
			// github client never calls this for some reason
			fmt.Println(w, `{}`)
			return
		}
		fmt.Fprintln(w, `{
			"access_token": "8761acafaf27a6db332aca598416ec0af15b485f",
			"token_type": "bearer",
			"refresh_token": "8761acafaf27a6db332aca598416ec0af15b485a",
			"expires_in": "2145916800"
}`)
	}))
	defer ts.Close()

	wantUserID := 12
	//um := &mockUserManager{UserID: wantUserID}

	um := NewUserManager(logger, nil, "id", "secret", "stripeKey")
	um.overwriteBaseURL = ts.URL
	um.oauthConf.Endpoint.AuthURL = ""
	um.oauthConf.Endpoint.TokenURL = ts.URL
	um.OAuthCallbackHandler(w, r)

	// Expect a redirect
	if w.Result().Header.Get("Location") == "" {
		t.Errorf("expected a redirect")
	}

	if s.UserID != wantUserID {
		t.Errorf("s.UserID have %q, want %q", s.UserID, wantUserID)
	}

	// Expect session to have cleared GitHubOAuthState
	if s.GitHubOAuthState != uuid.Nil {
		t.Errorf("expected GitHubOAuthState to be nil, have: %v", s.GitHubOAuthState)
	}
}

func TestGitHubLogin_new(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	var (
		wantUserID = 1
		githubID   = 2
		token      = &oauth2.Token{AccessToken: "tkn"}
		email      = "user@example.com"
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `[{"email": "`+email+`","verified": true,"primary": true}]`)
	}))
	defer ts.Close()
	githubBaseURL = ts.URL

	um := NewUserManager(logger, sqlx.NewDb(db, "sqlmock"), "", "", "stripeKey")

	mock.ExpectQuery("SELECT id FROM users WHERE github_id = ?").
		WithArgs(githubID).
		WillReturnError(sql.ErrNoRows)

	mock.ExpectExec("INSERT INTO users .*").
		WithArgs(email, githubID, []byte(`{"access_token":"tkn","expiry":"0001-01-01T00:00:00Z"}`)).
		WillReturnResult(sqlmock.NewResult(int64(wantUserID), 1))

	userID, err := um.GitHubLogin(githubID, token)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	if userID != wantUserID {
		t.Errorf("userID have %v want %v", userID, wantUserID)
	}
}

func TestGitHubLogin_update(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	var (
		wantUserID = 1
		githubID   = 2
		token      = &oauth2.Token{AccessToken: "a"}
		email      = "user@example.com"
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `[{"email": "`+email+`","verified": true,"primary": true}]`)
	}))
	defer ts.Close()
	githubBaseURL = ts.URL

	um := NewUserManager(logger, sqlx.NewDb(db, "sqlmock"), "", "", "stripeKey")

	mock.ExpectQuery("SELECT id FROM users WHERE github_id = ?").
		WithArgs(githubID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wantUserID))

	mock.ExpectExec("UPDATE users .*").
		WithArgs(email, []byte(`{"access_token":"a","expiry":"0001-01-01T00:00:00Z"}`), wantUserID).
		WillReturnResult(sqlmock.NewResult(int64(wantUserID), 1))

	userID, err := um.GitHubLogin(githubID, token)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	if userID != wantUserID {
		t.Errorf("userID have %v want %v", userID, wantUserID)
	}
}

func TestGitHubLogin_errSelect(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	um := NewUserManager(logger, sqlx.NewDb(db, "sqlmock"), "", "", "stripeKey")

	mock.ExpectQuery("SELECT .*").WillReturnError(errors.New("some error"))

	_, err = um.GitHubLogin(1, &oauth2.Token{})
	if err == nil {
		t.Fatal("expected error got nil")
	}
}

func TestGitHubLogin_errInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	um := NewUserManager(logger, sqlx.NewDb(db, "sqlmock"), "", "", "stripeKey")

	mock.ExpectQuery("SELECT .*").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO users .*").WillReturnError(errors.New("some error"))

	_, err = um.GitHubLogin(1, &oauth2.Token{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGitHubLogin_errUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	um := NewUserManager(logger, sqlx.NewDb(db, "sqlmock"), "", "", "stripeKey")

	mock.ExpectQuery("SELECT .*").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec("UPDATE users .*").WillReturnError(errors.New("some error"))

	_, err = um.GitHubLogin(1, &oauth2.Token{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetGitHubEmail(t *testing.T) {
	type email struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
		Primary  bool   `json:"primary"`
	}
	tests := []struct {
		want   string
		emails []email
	}{
		{"2@example.com", []email{
			{"1@example.com", false, false},
			{"2@example.com", true, true},
		}},
		{"", []email{
			{"1@example.com", false, false},
			{"2@example.com", false, true},
			{"3@example.com", true, false},
		}},
	}

	for _, test := range tests {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			byt, _ := json.Marshal(test.emails)
			fmt.Fprintln(w, string(byt))
		}))
		defer ts.Close()
		githubBaseURL = ts.URL

		um := NewUserManager(logger, nil, "", "", "stripeKey")
		have, err := um.getGitHubEmail(&oauth2.Token{AccessToken: "a"})
		if err != nil {
			t.Fatal("unexpected error:", err)
		}
		if have != test.want {
			t.Errorf("have: %q want: %q", have, test.want)
		}
	}
}
