package gopherci

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	sqlmock "github.com/bradleyfalzon/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestListInstallations_errors(t *testing.T) {
	someErr := errors.New("some error")
	tests := []struct {
		sqlErr  error
		wantErr error
		wantIns []Installation
	}{
		{sql.ErrNoRows, nil, nil},
		{someErr, someErr, nil},
	}

	for _, test := range tests {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}
		defer db.Close()

		accountIDs := []int{1, 2}

		mock.ExpectQuery(`SELECT.*FROM gh_installations WHERE account_id IN \(\?, \?\)`).
			WithArgs(accountIDs[0], accountIDs[1]).
			WillReturnError(test.sqlErr)

		client := New(sqlx.NewDb(db, "sqlmock"))
		installations, err := client.ListInstallations(accountIDs...)
		if err != test.wantErr {
			t.Errorf("unexpected error have: %+v want: %+v ", err, test.wantErr)
		}
		if !reflect.DeepEqual(installations, test.wantIns) {
			t.Errorf("unexpected result have: %+v want: %+v ", installations, test.wantIns)
		}
	}
}

func TestListInstallations(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	accountIDs := []int{1, 2}

	rows := sqlmock.NewRows([]string{"installation_id", "account_id"}).AddRow(1, 1).AddRow(2, 2)

	mock.ExpectQuery(`SELECT.*FROM gh_installations WHERE account_id IN \(\?, \?\)`).
		WithArgs(accountIDs[0], accountIDs[1]).
		WillReturnRows(rows)

	client := New(sqlx.NewDb(db, "sqlmock"))
	installations, err := client.ListInstallations(accountIDs...)
	if err != nil {
		t.Error("unexpected error: ", err)
	}

	want := []Installation{{1, 1}, {2, 2}}
	if !reflect.DeepEqual(installations, want) {
		t.Errorf("have %+v want %+v", installations, want)
	}
}

func TestEnableInstallation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	client := New(sqlx.NewDb(db, "sqlmock"))

	const installationID = 1

	mock.ExpectExec("UPDATE gh_installations SET enabled_at = NOW() WHERE installation_id = ?").
		WithArgs(installationID)

	err = client.EnableInstallation(installationID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDisableInstallation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	client := New(sqlx.NewDb(db, "sqlmock"))

	const installationID = 1

	mock.ExpectExec("UPDATE gh_installations SET enabled_at = NULL WHERE installation_id = ?").
		WithArgs(installationID)

	err = client.DisableInstallation(installationID)
	if err == nil {
		t.Fatal("expected error")
	}
}
