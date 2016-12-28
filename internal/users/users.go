package users

import (
	"database/sql"
	"fmt"

	"github.com/pkg/errors"
)

// UserManager manages all the user accounts.
type UserManager struct {
	db *sql.DB
}

// NewUserManager returns a new UserManager.
func NewUserManager(db *sql.DB) *UserManager {
	return &UserManager{db: db}
}

// GitHubLogin assigns the token to an existing user with the given githubID,
// if the user does not exist, the user is created. If an error occurs err is
// non-nil, else the userID of the user is returned.
func (um *UserManager) GitHubLogin(githubID int, token string) (userID int, err error) {
	err = um.db.QueryRow("SELECT id FROM users WHERE github_id = ?", githubID).Scan(&userID)
	switch {
	case err == sql.ErrNoRows:
		// Add token to new user
		res, err := um.db.Exec("INSERT INTO users (github_id, github_token) VALUES (?, ?)", githubID, token)
		if err != nil {
			return 0, errors.Wrap(err, fmt.Sprintf("error inserting new githubID %q", githubID))
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, errors.Wrap(err, "error in lastInsertId")
		}
		return int(id), nil
	case err != nil:
		return 0, errors.Wrap(err, fmt.Sprintf("error getting userID for githubID %q", githubID))
	}

	// Add token to existing user
	_, err = um.db.Exec("UPDATE users SET github_token = ? WHERE id = ?", token, userID)
	if err != nil {
		return 0, errors.Wrap(err, fmt.Sprintf("could set userID %q github_token", userID))
	}
	return userID, nil
}
