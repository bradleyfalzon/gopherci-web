package github

import (
	"log"
	"net/http"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/google/uuid"

	"golang.org/x/oauth2"
	ghoauth "golang.org/x/oauth2/github"
)

// GitHub handles GitHub specific tasks, such as OAuth.
type GitHub struct {
	oauthConf *oauth2.Config
}

// New returns a new GitHub initialised with clientID and clientSecret.
func New(clientID, clientSecret string) *GitHub {
	return &GitHub{
		oauthConf: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     ghoauth.Endpoint,
		},
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
		log.Print("github: invalid oauth state is nil")
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return 0, nil
	}

	state := r.FormValue("state")
	if state != session.GitHubOAuthState.String() {
		log.Printf("github: invalid oauth state, have %q, want %q", state, session.GitHubOAuthState.String())
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return 0, nil
	}
	session.GitHubOAuthState = uuid.Nil

	code := r.FormValue("code")
	token, err := g.oauthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Println("github: oauthConf exchange() error:", err)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return 0, nil
	}

	// Create a user with our id, githubid (many people could have the same id? can people authenticate as an
	// organisation?) and their token
	// If user already exists, update that user's token.

	// store this token?
	_ = token

	//oauthClient := g.oauthConf.Client(oauth2.NoContext, token)
	//client := github.NewClient(oauthClient)
	//user, _, err := client.Users.Get("")
	//if err != nil {
	//log.Println("github: could not get user:", err)
	//http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
	//return 0, nil
	//}
	//log.Printf("Logged in as GitHub user: %s\n", *user.Login)
	http.Redirect(w, r, "/members", http.StatusTemporaryRedirect)
	return 0, nil
}
