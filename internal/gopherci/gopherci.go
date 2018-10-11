// Package gopherci is a client library for GopherCI, it should be refactored
// into GopherCI itself, and used as a library but offer no backwards
// compatibility guarantees.
package gopherci

import (
	"database/sql"

	"github.com/jmoiron/sqlx"
)

// Client is a GopherCI client used to interact with internal GopherCI database.
type Client struct {
	db *sqlx.DB
}

// New returns a GopherCI client given a db and driver.
func New(db *sqlx.DB) *Client {
	return &Client{db: db}
}

// Installation represents a row from the gh_installations table.
type Installation struct {
	InstallationID int   `db:"installation_id"`
	AccountID      int64 `db:"account_id"`
}

// ListInstallations returns a slice of installations matching accountIDs, if
// no rows matched, installations is nil.
func (c *Client) ListInstallations(accountIDs ...int64) ([]Installation, error) {
	query, args, err := sqlx.In("SELECT installation_id, account_id FROM gh_installations WHERE account_id IN (?)", accountIDs)
	if err != nil {
		return nil, err
	}
	var installations []Installation
	err = c.db.Select(&installations, query, args...)
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, err
	}
	return installations, nil
}

// EnableInstallation enables an installationID in GopherCI's DB.
func (c *Client) EnableInstallation(installationID int) error {
	_, err := c.db.Exec("UPDATE gh_installations SET enabled_at = NOW() WHERE installation_id = ?", installationID)
	return err
}

// DisableInstallation disables an installationID in GopherCI's DB.
func (c *Client) DisableInstallation(installationID int) error {
	_, err := c.db.Exec("UPDATE gh_installations SET enabled_at = NULL WHERE installation_id = ?", installationID)
	return err
}
