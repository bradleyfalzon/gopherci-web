package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/bradleyfalzon/gopherci-web/internal/users"
	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
)

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
	s, err := session.GetOrCreate(db, w, r)
	if err != nil {
		log.Println("error: could not initialise session:", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), session.CtxKey{}, s))

	// Execute handler
	if code, err := fn(w, r); err != nil {
		if code >= http.StatusInternalServerError {
			log.Printf("internal error type %T: %+v", err, err)
			err = errors.New("Internal error")
		}
		errorHandler(w, r, code, err)
	}

	// Save session even if errors occurred
	if err := s.Save(); err != nil {
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

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("internal error: %+v", err)
	errorHandler(w, r, http.StatusInternalServerError, errors.New("Internal Error"))
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

// consoleIndex displays the console's index page
func consoleIndexHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	type install struct {
		AccountID      int // github accountID, may not be set on orphaned installations
		InstallationID int
		Type           string
		Name           string
		CanDisable     bool // allows the user to disable the installation
		State          string
	}
	page := struct {
		Title           string
		Email           string
		Installs        []install
		HasSubscription bool
	}{Title: "Console"}

	// Check if logged in
	// TODO this should be a part of middleware
	session := session.FromContext(r.Context())
	if !session.LoggedIn() {
		http.Redirect(w, r, "/gh/login", http.StatusFound)
		return 0, nil
	}

	user, err := um.GetUser(session.UserID)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	page.Email = user.Email

	ghUser, _, err := user.GHClient.Users.Get("")
	if err != nil {
		return http.StatusInternalServerError, err
	}

	// Get list of organisations from github api for this user
	ghMemberships, _, err := user.GHClient.Organizations.ListOrgMemberships(&github.ListOrgMembershipsOptions{State: "active"})
	if err != nil {
		return http.StatusInternalServerError, err
	}

	ei, err := user.EnabledInstallations()
	if err != nil {
		return http.StatusInternalServerError, err
	}
	enabledInstallations := make(map[int]bool)
	for _, installationID := range ei {
		enabledInstallations[installationID] = true
	}

	// Refactor the following into gciweb package? Yes

	accountIDs := []int{*ghUser.ID}
	page.Installs = append(page.Installs, install{
		AccountID:  *ghUser.ID,
		Type:       "Personal",
		Name:       *ghUser.Login,
		CanDisable: true,
	})

	for _, m := range ghMemberships {
		accountIDs = append(accountIDs, *m.Organization.ID)

		install := install{
			AccountID: *m.Organization.ID,
			Type:      "Organisation",
			Name:      *m.Organization.Login,
		}

		page.Installs = append(page.Installs, install)
	}

	// Check if any installations are pending
	gciInstalls, err := gciClient.ListInstallations(accountIDs...)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	// Compare User's installations with installations in GopherCI DB
	for i := range page.Installs {
		page.Installs[i].State = "New"
		for _, gciInstall := range gciInstalls {
			if gciInstall.AccountID != page.Installs[i].AccountID {
				continue
			}

			page.Installs[i].InstallationID = gciInstall.InstallationID
			page.Installs[i].State = "Disabled"
			if _, ok := enabledInstallations[gciInstall.InstallationID]; ok {
				page.Installs[i].State = "Enabled"

				// remove installation to track which instalaltions are orphaned
				delete(enabledInstallations, gciInstall.InstallationID)
			}
		}
	}

	// We also need to track if a user has cancelled a subscription, to disable
	// all installations.

	// Also need a script to check when an installation has been uninstalled
	// the installation is disabled for the user.

	// Installs enabled, but user no long has access to (i.e. removed from org)
	for installationID := range enabledInstallations {
		// Ideally GitHub would provide an API to get information about an installation
		// without us having to track it ourselves.
		page.Installs = append(page.Installs, install{
			InstallationID: installationID,
			Type:           "Orphaned",
			Name:           fmt.Sprintf("Unknown, Installation ID %v", installationID),
			State:          "Enabled",
		})
	}

	subs, err := user.StripeSubscriptions()
	if err != nil {
		return http.StatusInternalServerError, err
	}
	for _, sub := range subs {
		if sub.CancelledAt.IsZero() {
			page.HasSubscription = true
		}
	}

	if err := templates.ExecuteTemplate(w, "console-index.tmpl", page); err != nil {
		log.Println("error parsing console-index template:", err)
	}
	return http.StatusOK, nil
}

// consoleInstallStateHandler enables or disabled an installation
func consoleInstallStateHandler(w http.ResponseWriter, r *http.Request) (int, error) {

	// Check if logged in
	// TODO this should be a part of middleware (MustBeUser)
	session := session.FromContext(r.Context())
	if !session.LoggedIn() {
		http.Redirect(w, r, "/", http.StatusFound)
		return 0, nil
	}

	// TODO maybe this should be handled in MustBeUser handler
	user, err := um.GetUser(session.UserID)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	r.ParseForm()

	i, err := strconv.ParseInt(r.FormValue("installationID"), 10, 64)
	if err != nil {
		return http.StatusBadRequest, err
	}
	installationID := int(i)

	switch r.FormValue("state") {
	case "enable":
		err = user.EnableInstallation(installationID)
		if err == nil {
			err = gciClient.EnableInstallation(installationID)
		}
	case "disable":
		if !user.InstallationEnabled(installationID) {
			return http.StatusForbidden, errors.New("Installation not enabled for this user")
		}
		err = user.DisableInstallation(installationID)
		if err == nil {
			err = gciClient.DisableInstallation(installationID)
		}
	default:
		return http.StatusBadRequest, errors.New("Invalid state")
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}

	http.Redirect(w, r, "/console/", http.StatusFound)
	return 0, nil
}

// consolePaymentsHandler manages plans.
func consolePaymentsHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	page := struct {
		Title            string
		Email            string
		StripePublishKey string
		Subscriptions    []users.Subscription
		HasSubscription  bool
	}{Title: "Payments", StripePublishKey: os.Getenv("STRIPE_PUBLISH_KEY")}

	// Check if logged in
	// TODO this should be a part of middleware (MustBeUser)
	session := session.FromContext(r.Context())
	if !session.LoggedIn() {
		http.Redirect(w, r, "/", http.StatusFound)
		return 0, nil
	}

	// TODO maybe this should be handled in MustBeUser handler
	user, err := um.GetUser(session.UserID)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	page.Email = user.Email

	page.Subscriptions, err = user.StripeSubscriptions()
	if err != nil {
		return http.StatusInternalServerError, err
	}

	for _, sub := range page.Subscriptions {
		if sub.CancelledAt.IsZero() {
			page.HasSubscription = true
		}
	}

	if err := templates.ExecuteTemplate(w, "console-payments.tmpl", page); err != nil {
		log.Println("error parsing console-payments template:", err)
	}
	return http.StatusOK, nil
}

// consolePaymentsProcessHandler processes the results of a payment (not the
// payment itself).
func consolePaymentsProcessHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	vars := mux.Vars(r)

	// Check if logged in
	// TODO this should be a part of middleware (MustBeUser)
	session := session.FromContext(r.Context())
	if !session.LoggedIn() {
		http.Redirect(w, r, "/", http.StatusFound)
		return 0, nil
	}

	// TODO maybe this should be handled in MustBeUser handler
	user, err := um.GetUser(session.UserID)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	r.ParseForm()

	// Ensure customer does not have any current subscriptions until we have the
	// ability to prorata subscriptions.
	subs, err := user.StripeSubscriptions()
	if err != nil {
		return http.StatusInternalServerError, err
	}
	for _, sub := range subs {
		if sub.CancelledAt.IsZero() {
			// TODO flash message
			return http.StatusBadRequest, errors.New("active subscription already exists")
		}
	}

	err = user.ProcessStripePayment(r.Form.Get("stripeToken"), vars["planID"])
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "could not process stripe payment")
	}

	log.Printf("processed stripe subscription for userID %v on plan %v", user.UserID, vars["planID"])

	http.Redirect(w, r, "/console/payments", http.StatusFound)
	return 0, nil
}

// consolePaymentsCancelHandler cancels a subscription.
func consolePaymentsCancelHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	// Check if logged in
	// TODO this should be a part of middleware (MustBeUser)
	session := session.FromContext(r.Context())
	if !session.LoggedIn() {
		http.Redirect(w, r, "/", http.StatusFound)
		return 0, nil
	}

	// TODO maybe this should be handled in MustBeUser handler
	user, err := um.GetUser(session.UserID)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	r.ParseForm()
	subs, err := user.StripeSubscriptions()
	if err != nil {
		return http.StatusInternalServerError, err
	}
	var sub *users.Subscription
	for _, s := range subs {
		if s.ID == r.Form.Get("subscriptionID") {
			sub = &s
			break
		}
	}
	if sub == nil {
		// TODO flash message to show user we couldn't find subscription ID
		return http.StatusBadRequest, errors.New("could not find subscription ID")
	}

	err = user.CancelStripeSubscription(r.Form.Get("subscriptionID"), true)
	if err != nil {
		return http.StatusInternalServerError, errors.Wrap(err, "could not process stripe payment")
	}

	// TODO disable all integrations at sub.PeriodEnd #7

	log.Printf("cancelled stripe subscription for userID %v subscriptionID %q", user.UserID, r.Form.Get("subscriptionID"))

	http.Redirect(w, r, "/console/payments", http.StatusFound)
	return 0, nil
}
