package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	sqlmock "github.com/bradleyfalzon/go-sqlmock"
	"github.com/google/uuid"
)

func TestGetOrCreate_create(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	s, err := GetOrCreate(db, w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	if s == nil {
		t.Fatal("expected session, got nil")
	}

	if w.Result().Header.Get("Set-Cookie") == "" {
		t.Fatal("set-cookie header not sent")
	}
}

func TestGetOrCreate_get(t *testing.T) {
	const sid = "7a6e02a0-5ef8-43f9-95f5-2708863cc753"
	var jsonData = []byte(`{"GitHubID":1}`)
	sidArray := []uint8{122, 110, 2, 160, 94, 248, 67, 249, 149, 245, 39, 8, 134, 60, 199, 83}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: sid,
	})
	w := httptest.NewRecorder()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT json FROM sessions WHERE id=?").
		WithArgs(sidArray).
		WillReturnRows(sqlmock.NewRows([]string{"json"}).AddRow(jsonData))

	s, err := GetOrCreate(db, w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	want := &Session{
		db:       db,
		id:       uuid.Must(uuid.Parse(sid)),
		json:     jsonData,
		GitHubID: 1,
	}

	if !reflect.DeepEqual(s, want) {
		t.Errorf("\nhave: %#v\nwant: %#v", s, want)
	}

	if w.Result().Header.Get("Set-Cookie") != "" {
		t.Fatal("set-cookie header was sent and not expected")
	}
}

func TestGetOrCreate_cannotParse(t *testing.T) {
	const sid = "invalid"

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: sid,
	})
	w := httptest.NewRecorder()

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	s, err := GetOrCreate(db, w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	if s == nil {
		t.Fatal("expected session, got nil")
	}

	if w.Result().Header.Get("Set-Cookie") == "" {
		t.Fatal("set-cookie header not sent")
	}
}

func TestGetOrCreate_sqlErrNoRows(t *testing.T) {
	const sid = "7a6e02a0-5ef8-43f9-95f5-2708863cc753"

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: sid,
	})
	w := httptest.NewRecorder()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	mock.ExpectQuery(".*").WillReturnError(sql.ErrNoRows)

	s, err := GetOrCreate(db, w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	if s == nil {
		t.Fatal("expected session, got nil")
	}

	if w.Result().Header.Get("Set-Cookie") == "" {
		t.Fatal("set-cookie header not sent")
	}
}

func TestGetOrCreate_sqlOtherErr(t *testing.T) {
	const sid = "7a6e02a0-5ef8-43f9-95f5-2708863cc753"

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: sid,
	})
	w := httptest.NewRecorder()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	mock.ExpectQuery(".*").WillReturnError(errors.New("some error"))

	s, err := GetOrCreate(db, w, r)
	if err == nil {
		t.Fatal("expected error got: ", err)
	}

	if s != nil {
		t.Fatal("expected session to be nil")
	}

	if w.Result().Header.Get("Set-Cookie") != "" {
		t.Fatal("set-cookie header was sent and not expected")
	}
}

func TestGetOrCreate_notJSON(t *testing.T) {
	const sid = "7a6e02a0-5ef8-43f9-95f5-2708863cc753"

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: sid,
	})
	w := httptest.NewRecorder()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT json FROM sessions WHERE id=?").
		WillReturnRows(sqlmock.NewRows([]string{"json"}).AddRow([]byte(`notjson`)))

	s, err := GetOrCreate(db, w, r)
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}

	if s == nil {
		t.Fatal("expected session, got nil")
	}

	if w.Result().Header.Get("Set-Cookie") == "" {
		t.Fatal("set-cookie header not sent")
	}
}

func TestSave(t *testing.T) {
	const sid = "7a6e02a0-5ef8-43f9-95f5-2708863cc753"

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal("unexpected error: ", err)
	}
	defer db.Close()

	s := &Session{
		db:       db,
		id:       uuid.Must(uuid.Parse(sid)),
		json:     []byte(`{"GitHubID":1}`),
		expires:  time.Unix(1, 1),
		GitHubID: 2, // GitHubID changed
	}

	jsonSession, _ := json.Marshal(s)

	mock.ExpectExec(`INSERT INTO sessions \(id, json, expires_at\) VALUES \(\?, \?, \?\) ON DUPLICATE KEY UPDATE json = \?`).
		WithArgs(s.id[:], jsonSession, s.expires, jsonSession).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.Save()
	if err != nil {
		t.Fatal("Unexpected error: ", err)
	}
}

func TestSave_noChanges(t *testing.T) {
	const sid = "7a6e02a0-5ef8-43f9-95f5-2708863cc753"

	s := &Session{
		db:      nil, // panic if this is used
		id:      uuid.Must(uuid.Parse(sid)),
		expires: time.Unix(1, 1),
	}
	s.json, _ = json.Marshal(s)

	err := s.Save()
	if err != nil {
		t.Fatal("Unexpected error: ", err)
	}
}
