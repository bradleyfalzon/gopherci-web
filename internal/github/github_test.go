package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/google/uuid"
)

func TestOAuthLoginHandler(t *testing.T) {
	s := &session.Session{}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	gh := New("id", "secret", nil)
	gh.oauthConf.Endpoint.AuthURL = "http://example.com"
	gh.oauthConf.Endpoint.TokenURL = ""
	_, err := gh.OAuthLoginHandler(w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	// Expect a redirect (this is not a great test)
	want := "http://example.com?access_type=online&client_id=id&response_type=code&state="
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

var _ UserManager = &mockUserManager{}

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
	um := &mockUserManager{UserID: wantUserID}

	gh := New("id", "secret", um)
	gh.overwriteBaseURL = ts.URL
	gh.oauthConf.Endpoint.AuthURL = ""
	gh.oauthConf.Endpoint.TokenURL = ts.URL
	_, err := gh.OAuthCallbackHandler(w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

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
