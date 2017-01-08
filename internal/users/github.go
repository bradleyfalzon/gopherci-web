package users

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/google/go-github/github"
	"github.com/google/uuid"
	"github.com/pkg/errors"

	"golang.org/x/oauth2"
)

// OAuthLoginHandler starts the initial oauth login flow by redirecting the
// user to GitHub for authentication and authorisation our app.
func (um *UserManager) OAuthLoginHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	session := session.FromContext(r.Context())
	session.GitHubOAuthState = uuid.New()

	url := um.oauthConf.AuthCodeURL(session.GitHubOAuthState.String(), oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	return 0, nil
}

// OAuthCallbackHandler handles the callback after GitHub authentication and
// persists the credentials to storage for use later.
func (um *UserManager) OAuthCallbackHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	session := session.FromContext(r.Context())

	if session.GitHubOAuthState == uuid.Nil {
		return http.StatusBadRequest, errors.New("github: invalid oauth state from session, it is nil")
	}

	state := r.FormValue("state")
	if state != session.GitHubOAuthState.String() {
		return http.StatusBadRequest, fmt.Errorf("github: invalid oauth state from session, have %q, want %q", state, session.GitHubOAuthState.String())
	}
	session.GitHubOAuthState = uuid.Nil

	code := r.FormValue("code")
	token, err := um.oauthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "github: oauthConf exchange() error")
	}

	client := NewClient(um.oauthConf, token)
	ghUser, _, err := client.Users.Get("")
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "github: could not get user")
	}
	log.Printf("github: logged in as GitHub user: %s\n", *ghUser.Login)

	// Set session to this user
	id, err := um.GitHubLogin(*ghUser.ID, token)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "github: could not set github user in db")
	}
	session.UserID = id

	http.Redirect(w, r, "/console/", http.StatusTemporaryRedirect)
	return 0, nil
}

// NewClient returns a github.Client using oauthconf and token.
func NewClient(oauthConf *oauth2.Config, token *oauth2.Token) *github.Client {
	oauthClient := oauthConf.Client(oauth2.NoContext, token)
	client := github.NewClient(oauthClient)
	//if um.overwriteBaseURL != "" {
	//client.BaseURL, _ = url.Parse(um.overwriteBaseURL)
	//}
	return client
}

// GitHubLogin assigns the token to an existing user with the given githubID,
// if the user does not exist, the user is created. If an error occurs err is
// non-nil, else the userID of the user is returned.
func (um *UserManager) GitHubLogin(githubID int, token *oauth2.Token) (userID int, err error) {
	jsonToken, err := json.Marshal(token)
	if err != nil {
		return 0, errors.Wrap(err, "could not marshal oauth2.token")
	}

	// Get user's email
	client := NewClient(um.oauthConf, token)
	emails, _, err := client.Users.ListEmails(nil)
	if err != nil {
		return 0, errors.Wrap(err, "error getting email for new user")
	}
	var email string
	for _, e := range emails {
		if *e.Primary && *e.Verified {
			email = *e.Email
			break
		}
	}
	if email == "" {
		return 0, errors.New("could not get user's primary verified email from GitHub")
	}

	err = um.db.QueryRow("SELECT id FROM users WHERE github_id = ?", githubID).Scan(&userID)
	switch {
	case err == sql.ErrNoRows:
		// Add token to new user
		res, err := um.db.Exec("INSERT INTO users (email, github_id, github_token) VALUES (?, ?, ?)", email, githubID, jsonToken)
		if err != nil {
			return 0, errors.Wrapf(err, "error inserting new githubID %q", githubID)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, errors.Wrap(err, "error in lastInsertId")
		}
		return int(id), nil
	case err != nil:
		return 0, errors.Wrapf(err, "error getting userID for githubID %q", githubID)
	}

	// Add token to existing user and update email
	_, err = um.db.Exec("UPDATE users SET email = ?, github_token = ? WHERE id = ?", email, jsonToken, userID)
	if err != nil {
		return 0, errors.Wrapf(err, "could set userID %q github_token", userID)
	}
	return userID, nil
}
