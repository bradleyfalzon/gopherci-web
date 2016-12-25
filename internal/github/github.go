package github

import (
	"fmt"
	"log"
	"net/http"

	"github.com/google/go-github/github"

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

const xTODOFIXME = "TODOFIXME"

// OAuthLoginHandler starts the initial oauth login flow by redirecting the
// user to GitHub for authentication and authorisation our app.
func (g *GitHub) OAuthLoginHandler(w http.ResponseWriter, r *http.Request) {
	url := g.oauthConf.AuthCodeURL(xTODOFIXME, oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// OAuthCallbackHandler handles the callback after GitHub authentication and
// persists the credentials to storage for use later.
func (g *GitHub) OAuthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	state := r.FormValue("state")
	if state != xTODOFIXME {
		log.Printf("github: invalid oauth state, have %q, want %q", xTODOFIXME, state)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	code := r.FormValue("code")
	token, err := g.oauthConf.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Println("github: oauthConf exchange() error:", err)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	oauthClient := g.oauthConf.Client(oauth2.NoContext, token)
	client := github.NewClient(oauthClient)
	user, _, err := client.Users.Get("")
	if err != nil {
		log.Println("github: could not get user:", err)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	fmt.Printf("Logged in as GitHub user: %s\n", *user.Login)
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}
