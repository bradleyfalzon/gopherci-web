package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/google/go-github/github"
	"github.com/google/uuid"
	"github.com/pkg/errors"

	"golang.org/x/oauth2"
)

// githubBaseURL is the baseURL for github.com/google/go-github/github
// client, variable to easily change in tests.
var githubBaseURL = "https://api.github.com/"

// OAuthLoginHandler starts the initial oauth login flow by redirecting the
// user to GitHub for authentication and authorisation our app.
func (um *UserManager) OAuthLoginHandler(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	sess.GitHubOAuthState = uuid.New()

	uri := um.oauthConf.AuthCodeURL(sess.GitHubOAuthState.String(), oauth2.AccessTypeOnline)
	http.Redirect(w, r, uri, http.StatusTemporaryRedirect)
}

// OAuthCallbackHandler handles the callback after GitHub authentication and
// persists the credentials to storage for use later.
func (um *UserManager) OAuthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())

	if sess.GitHubOAuthState == uuid.Nil {
		um.logger.Error("github: invalid oauth state from sess, it is nil")
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	state := r.FormValue("state")
	if state != sess.GitHubOAuthState.String() {
		um.logger.Errorf("github: invalid oauth state from sess, have %q, want %q", state, sess.GitHubOAuthState.String())
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	sess.GitHubOAuthState = uuid.Nil

	code := r.FormValue("code")
	token, err := um.oauthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		um.logger.WithError(err).Error("github: oauthConf exchange() error")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	client := NewClient(um.oauthConf, token)
	ghUser, _, err := client.Users.Get(r.Context(), "")
	if err != nil {
		um.logger.WithError(err).Error("github: could not get user")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Set sess to this user
	sess.UserID, err = um.GitHubLogin(r.Context(), *ghUser.ID, token)
	if err != nil {
		um.logger.WithError(err).Error("github: could not set github user in db")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	um.logger.WithField("userID", sess.UserID).Printf("github: logged in as GitHub user: %s", *ghUser.Login)

	http.Redirect(w, r, "/console", http.StatusTemporaryRedirect)
}

// NewClient returns a github.Client using oauthconf and token.
func NewClient(oauthConf *oauth2.Config, token *oauth2.Token) *github.Client {
	oauthClient := oauthConf.Client(oauth2.NoContext, token)
	client := github.NewClient(oauthClient)
	client.BaseURL, _ = url.Parse(githubBaseURL)
	//if um.overwriteBaseURL != "" {
	//client.BaseURL, _ = url.Parse(um.overwriteBaseURL)
	//}
	return client
}

// GitHubLogin assigns the token to an existing user with the given githubID,
// if the user does not exist, the user is created. If an error occurs err is
// non-nil, else the userID of the user is returned.
func (um *UserManager) GitHubLogin(ctx context.Context, githubID int64, token *oauth2.Token) (userID int, err error) {
	jsonToken, err := json.Marshal(token)
	if err != nil {
		return 0, errors.Wrap(err, "could not marshal oauth2.token")
	}

	email, err := um.getGitHubEmail(ctx, token)
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

// getGetHubEmail returns the user's primary and verified email address, or
// blank if none found, or an error if an error occurred.
func (um *UserManager) getGitHubEmail(ctx context.Context, token *oauth2.Token) (string, error) {
	client := NewClient(um.oauthConf, token)
	emails, _, err := client.Users.ListEmails(ctx, nil)
	if err != nil {
		return "", errors.Wrap(err, "error getting email for new user")
	}
	var email string
	for _, e := range emails {
		if *e.Primary && *e.Verified {
			email = *e.Email
			break
		}
	}
	return email, nil
}
