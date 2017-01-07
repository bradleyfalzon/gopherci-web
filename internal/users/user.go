package users

import (
	"database/sql"
	"encoding/json"

	"golang.org/x/oauth2"

	"github.com/google/go-github/github"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

// User represents a GopherCI-web user.
type User struct {
	db          *sqlx.DB
	GHClient    *github.Client
	UserID      int    `db:"id"`
	Email       string `db:"email"`
	GitHubID    int    `db:"github_id"`
	GitHubToken []byte `db:"github_token"`
}

// GetUser looks up a user in the db and returns it, if no user was found,
// user is nil, if an error occurs it will be returned.
func GetUser(db *sqlx.DB, oauthConf *oauth2.Config, userID int) (*User, error) {
	user := &User{db: db}
	err := db.Get(user, "SELECT id, email, github_id, github_token FROM users WHERE id = ?", userID)
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, errors.Wrap(err, "could not select from users")
	}
	var token oauth2.Token
	err = json.Unmarshal(user.GitHubToken, &token)
	if err != nil {
		return nil, err
	}
	user.GitHubToken = nil
	user.GHClient = NewClient(oauthConf, &token)
	return user, nil
}

// GitHubListOrgMembershipsActive returns active
// https://godoc.org/github.com/google/go-github/github#OrganizationsService.ListOrgMemberships
func (u *User) GitHubListOrgMembershipsActive() ([]*github.Membership, error) {
	memberships, _, err := u.GHClient.Organizations.ListOrgMemberships(&github.ListOrgMembershipsOptions{State: "active"})
	if err != nil {
		return nil, err
	}
	return memberships, nil
}

// EnableInstallation marks a GitHub installation as enabled for this user.
// This does not enable the installation in GopherCI. Returns an error if an
// error occured, else success if successfully changed from disabled to
// enabled.
func (u *User) EnableInstallation(installationID int) error {
	// TODO check if they haven't exceeded any quotas first
	_, err := u.db.Exec(`INSERT IGNORE INTO gh_installations (user_id, installation_id) VALUES (?, ?)`, u.UserID, installationID)
	return err
}

// DisableInstallation marks a GitHub installation as disabled for this user.
// This does not disable the installation in GopherCI. Returns an error if an
// error occurred, else success if successfully changed from enabled to
// disabled.
func (u *User) DisableInstallation(installationID int) error {
	_, err := u.db.Exec(`DELETE FROM gh_installations WHERE user_id = ? AND installation_id = ?`, u.UserID, installationID)
	return err
}

// InstallationEnabled checks if installationID is enabled by this user, any error means
// installation is not enabled by this user.
func (u *User) InstallationEnabled(installationID int) bool {
	var installations int
	err := u.db.Get(&installations, `SELECT COUNT(*) FROM gh_installations WHERE user_id = ? AND installation_id = ?`, u.UserID, installationID)
	if err != nil {
		return false
	}
	return installations > 0
}

// EnabledInstallations returns a slice of installationIDs that are marked as
// enabled by for the user.
func (u *User) EnabledInstallations() ([]int, error) {
	installationIDs := []int{}
	err := u.db.Select(&installationIDs, `SELECT installation_id FROM gh_installations WHERE user_id = ?`, u.UserID)
	switch {
	case err == sql.ErrNoRows:
		return installationIDs, err
	case err != nil:
		return nil, err
	}
	return installationIDs, nil
}
