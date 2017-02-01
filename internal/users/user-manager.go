package users

import (
	"golang.org/x/oauth2"

	"github.com/Sirupsen/logrus"
	"github.com/jmoiron/sqlx"
	stripe "github.com/stripe/stripe-go"

	ghoauth "golang.org/x/oauth2/github"
)

// UserManager manages all the user accounts.
type UserManager struct {
	logger           *logrus.Entry
	db               *sqlx.DB
	oauthConf        *oauth2.Config
	overwriteBaseURL string // used to overwrite baseURL for testing
}

// NewUserManager returns a new UserManager initialised with db and GitHub
// clientID and clientSecret.
func NewUserManager(logger *logrus.Entry, db *sqlx.DB, clientID, clientSecret, stripeKey string) *UserManager {
	stripe.Key = stripeKey
	return &UserManager{
		logger: logger,
		db:     db,
		oauthConf: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     ghoauth.Endpoint,
			Scopes:       []string{"user:email", "read:org"},
		},
	}
}

// GetUser returns a user for a given UserID, returns nil if user is not found
// or an error.
func (um *UserManager) GetUser(userID int) (*User, error) {
	return GetUser(um.logger, um.db, um.oauthConf, userID)
}
