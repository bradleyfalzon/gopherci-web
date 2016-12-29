package users

import (
	"golang.org/x/oauth2"

	"github.com/jmoiron/sqlx"

	ghoauth "golang.org/x/oauth2/github"
)

// UserManager manages all the user accounts.
type UserManager struct {
	db               *sqlx.DB
	oauthConf        *oauth2.Config
	overwriteBaseURL string // used to overwrite baseURL for testing
}

// NewUserManager returns a new UserManager initialised with db and GitHub
// clientID and clientSecret.
func NewUserManager(db *sqlx.DB, clientID, clientSecret string) *UserManager {
	return &UserManager{
		db: db,
		oauthConf: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     ghoauth.Endpoint,
			Scopes:       []string{"user", "read:org"},
		},
	}
}

// GetUser returns a user for a given UserID, returns nil if user is not found
// or an error.
func (um *UserManager) GetUser(userID int) (*User, error) {
	return GetUser(um.db, um.oauthConf, userID)
}
