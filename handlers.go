package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
)

type sessionCtxKey struct{} // key for session context

type appError struct {
	Error   error  // Message to log
	Message string // Message to show user (longer)
	Code    int    // HTTP Status Code
}

// All handlers must have the same signature as appHandler. If any errors occur
// handlers are expected to return an appropriate HTTP Code and an error. The
// error is displayed back to the user. If an error is returned, no output
// should be written to http.ResponseWriter, a error handler will handle this.
type appHandler func(http.ResponseWriter, *http.Request) (code int, err error)

func (fn appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Global headers
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Get Session and store in request's context
	session, err := session.GetOrCreate(db, w, r)
	if err != nil {
		log.Println("error: could not initialise session:", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), sessionCtxKey{}, session))

	// Execute handler
	if code, err := fn(w, r); err != nil {
		errorHandler(w, r, code, err)
	}

	// Save session even if errors occured
	if err := session.Save(); err != nil {
		log.Println("appHandler: could not save session:", err)
	}
}

// homeHandler displays the home page
func homeHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	page := struct {
		Title string
	}{"GopherCI"}

	if err := templates.ExecuteTemplate(w, "home.tmpl", page); err != nil {
		log.Println("error parsing home template:", err)
	}
	return http.StatusOK, nil
}

// notFoundHandler displays a 404 not found error
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	errorHandler(w, r, http.StatusNotFound, fmt.Errorf("%v not found", r.URL))
}

// errorHandler handles an error message, with an optional description
func errorHandler(w http.ResponseWriter, r *http.Request, code int, err error) {
	page := struct {
		Title  string
		Code   string // eg 400
		Status string // eg Bad Request
		Desc   string // eg Missing key foo
	}{fmt.Sprintf("%d - %s", code, http.StatusText(code)), strconv.Itoa(code), http.StatusText(code), err.Error()}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	if err := templates.ExecuteTemplate(w, "error.tmpl", page); err != nil {
		log.Println("error parsing error template:", err)
	}
}
