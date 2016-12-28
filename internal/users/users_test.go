package users

import (
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/bradleyfalzon/go-sqlmock"
)

func TestGitHubLogin_new(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	var (
		wantUserID = 1
		githubID   = 2
		token      = "token"
	)

	um := NewUserManager(db)

	mock.ExpectQuery("SELECT id FROM users WHERE github_id = ?").
		WithArgs(githubID).
		WillReturnError(sql.ErrNoRows)

	mock.ExpectExec("INSERT INTO users .*").
		WithArgs(githubID, token).
		WillReturnResult(sqlmock.NewResult(int64(wantUserID), 1))

	userID, err := um.GitHubLogin(githubID, token)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	if userID != wantUserID {
		t.Errorf("userID have %v want %v", userID, wantUserID)
	}
}

func TestGitHubLogin_update(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	var (
		wantUserID = 1
		githubID   = 2
		token      = "token"
	)

	um := NewUserManager(db)

	mock.ExpectQuery("SELECT id FROM users WHERE github_id = ?").
		WithArgs(githubID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wantUserID))

	mock.ExpectExec("UPDATE users .*").
		WithArgs(token, wantUserID).
		WillReturnResult(sqlmock.NewResult(int64(wantUserID), 1))

	userID, err := um.GitHubLogin(githubID, token)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	if userID != wantUserID {
		t.Errorf("userID have %v want %v", userID, wantUserID)
	}
}

func TestGitHubLogin_errSelect(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	um := NewUserManager(db)

	mock.ExpectQuery("SELECT .*").WillReturnError(errors.New("some error"))

	_, err = um.GitHubLogin(1, "token")
	if err == nil {
		t.Fatal("expected error got nil")
	}
}

func TestGitHubLogin_errInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	um := NewUserManager(db)

	mock.ExpectQuery("SELECT .*").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO users .*").WillReturnError(errors.New("some error"))

	_, err = um.GitHubLogin(1, "token")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGitHubLogin_errUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	um := NewUserManager(db)

	mock.ExpectQuery("SELECT .*").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec("UPDATE users .*").WillReturnError(errors.New("some error"))

	_, err = um.GitHubLogin(1, "token")
	if err == nil {
		t.Fatal("expected error")
	}
}
