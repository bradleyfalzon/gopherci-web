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

	gh := New("id", "secret")
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

func TestOAuthCallbackHandler(t *testing.T) {
	s := &session.Session{GitHubOAuthState: uuid.New()}
	r := httptest.NewRequest("GET", "/?state="+s.GitHubOAuthState.String(), nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{
			"access_token": "1",
			"token_type": "bearer",
			"refresh_token": "3",
			"expires_in": "2145916800"
}`)
	}))
	defer ts.Close()

	gh := New("id", "secret")
	gh.oauthConf.Endpoint.AuthURL = ""
	gh.oauthConf.Endpoint.TokenURL = ts.URL
	_, err := gh.OAuthCallbackHandler(w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	// Expect a redirect (this is not a great test)
	want := "/members"
	if w.Result().Header.Get("Location") != want {
		t.Errorf("Location header does not have match\nhave: %v\nwant: %v", w.Result().Header.Get("Location"), want)
	}

	// Expect session to have a GitHubOAuthState
	if s.GitHubOAuthState != uuid.Nil {
		t.Errorf("expected GitHubOAuthState to be nil, have: %v", s.GitHubOAuthState)
	}
}

func TestOAuthCallbackHandler_noState(t *testing.T) {
	s := &session.Session{GitHubOAuthState: uuid.Nil}
	r := httptest.NewRequest("GET", "/?state="+s.GitHubOAuthState.String(), nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	gh := New("id", "secret")
	_, err := gh.OAuthCallbackHandler(w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	// Expect a redirect
	want := "/"
	if w.Result().Header.Get("Location") != want {
		t.Errorf("Location header does not have match\nhave: %v\nwant: %v", w.Result().Header.Get("Location"), want)
	}
}

func TestOAuthCallbackHandler_invalid(t *testing.T) {
	s := &session.Session{GitHubOAuthState: uuid.New()}
	r := httptest.NewRequest("GET", "/?state=invalid", nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	gh := New("id", "secret")
	_, err := gh.OAuthCallbackHandler(w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	// Expect a redirect
	want := "/"
	if w.Result().Header.Get("Location") != want {
		t.Errorf("Location header does not have match\nhave: %v\nwant: %v", w.Result().Header.Get("Location"), want)
	}
}

func TestOAuthCallbackHandler_exchangeErr(t *testing.T) {
	s := &session.Session{GitHubOAuthState: uuid.New()}
	r := httptest.NewRequest("GET", "/?state="+s.GitHubOAuthState.String(), nil)
	r = r.WithContext(context.WithValue(context.Background(), session.CtxKey{}, s))
	w := httptest.NewRecorder()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	gh := New("id", "secret")
	gh.oauthConf.Endpoint.AuthURL = ""
	gh.oauthConf.Endpoint.TokenURL = ts.URL
	_, err := gh.OAuthCallbackHandler(w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	// Expect a redirect (this is not a great test)
	want := "/"
	if w.Result().Header.Get("Location") != want {
		t.Errorf("Location header does not have match\nhave: %v\nwant: %v", w.Result().Header.Get("Location"), want)
	}

	// Expect session to have a GitHubOAuthState
	if s.GitHubOAuthState != uuid.Nil {
		t.Errorf("expected GitHubOAuthState to be nil, have: %v", s.GitHubOAuthState)
	}
}
