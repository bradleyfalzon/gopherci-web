package github

import (
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/google/go-github/github"
	"github.com/google/uuid"
	"github.com/pkg/errors"

	"golang.org/x/oauth2"
	ghoauth "golang.org/x/oauth2/github"
)

// GitHub handles GitHub specific tasks, such as OAuth.
type GitHub struct {
	oauthConf        *oauth2.Config
	userManager      UserManager
	overwriteBaseURL string // used to overwrite baseURL for testing
}

type UserManager interface {
	GitHubLogin(githubUserID int, oauthToken string) (userID int, err error)
}

// New returns a new GitHub initialised with clientID and clientSecret.
func New(clientID, clientSecret string, userManager UserManager) *GitHub {
	return &GitHub{
		oauthConf: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     ghoauth.Endpoint,
		},
		userManager: userManager,
	}
}

// OAuthLoginHandler starts the initial oauth login flow by redirecting the
// user to GitHub for authentication and authorisation our app.
func (g *GitHub) OAuthLoginHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	session := r.Context().Value(session.CtxKey{}).(*session.Session)
	session.GitHubOAuthState = uuid.New()

	url := g.oauthConf.AuthCodeURL(session.GitHubOAuthState.String(), oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	return 0, nil
}

// OAuthCallbackHandler handles the callback after GitHub authentication and
// persists the credentials to storage for use later.
func (g *GitHub) OAuthCallbackHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	session := r.Context().Value(session.CtxKey{}).(*session.Session)

	if session.GitHubOAuthState == uuid.Nil {
		return http.StatusBadRequest, errors.New("github: invalid oauth state is nil")
	}

	state := r.FormValue("state")
	if state != session.GitHubOAuthState.String() {
		return http.StatusBadRequest, fmt.Errorf("github: invalid oauth state, have %q, want %q", state, session.GitHubOAuthState.String())
	}
	session.GitHubOAuthState = uuid.Nil

	code := r.FormValue("code")
	token, err := g.oauthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "github: oauthConf exchange() error")
	}

	oauthClient := g.oauthConf.Client(oauth2.NoContext, token)
	client := github.NewClient(oauthClient)
	if g.overwriteBaseURL != "" {
		client.BaseURL, _ = url.Parse(g.overwriteBaseURL)
	}
	ghUser, _, err := client.Users.Get("")
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "github: could not get user")
	}
	log.Printf("github: logged in as GitHub user: %s\n", *ghUser.Login)

	// Set session to this user
	id, err := g.userManager.GitHubLogin(*ghUser.ID, token.AccessToken)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "github: could not set github user in db")
	}
	session.UserID = id

	http.Redirect(w, r, "/members", http.StatusTemporaryRedirect)
	return 0, nil
}
