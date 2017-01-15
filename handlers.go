package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/bradleyfalzon/gopherci-web/internal/users"
	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/event"
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
	s, err := session.GetOrCreate(logger.WithField("pkg", "session"), db, w, r)
	if err != nil {
		logger.WithError(err).Error("could not get session")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), session.CtxKey{}, s))

	// Execute handler
	if code, err := fn(w, r); err != nil {
		if code >= http.StatusInternalServerError {
			logger.WithError(err).Errorf("internal error type %T: %+v", err, err)
			err = errors.New("internal error")
		}
		errorHandler(w, r, code, err)
	}

	// Save session even if errors occurred
	if err := s.Save(); err != nil {
		logger.WithError(err).Error("could not save session")
	}
}

// homeHandler displays the home page
func homeHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	page := struct {
		Title string
	}{"GopherCI"}

	if err := templates.ExecuteTemplate(w, "home.tmpl", page); err != nil {
		logger.WithError(err).Error("error parsing home template")
	}
	return http.StatusOK, nil
}

// notFoundHandler displays a 404 not found error
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	errorHandler(w, r, http.StatusNotFound, fmt.Errorf("%v not found", r.URL))
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	logger.WithError(err).Errorf("internal error: %+v", err)
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

	// TODO check accept header and respond in json if accepted instead of html

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	if err := templates.ExecuteTemplate(w, "error.tmpl", page); err != nil {
		logger.WithError(err).Error("error parsing error template")
	}
}

// stripeEventHandler handles stripe webhooks/events.
func stripeEventHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	dec := json.NewDecoder(r.Body)
	var hookEvent stripe.Event
	if err := dec.Decode(&hookEvent); err != nil {
		return http.StatusBadRequest, errors.New("could not decode stripe event")
	}

	// Check authenticity with stripe
	checkedEvent, err := event.Get(hookEvent.ID, nil)
	if err != nil {
		// client error or event doesn't exist
		checkedEvent = &hookEvent
		if serr, ok := err.(*stripe.Error); ok && serr.HTTPStatusCode != http.StatusNotFound {
			return http.StatusForbidden, errors.New("could not get stripe event")
		}
		logger.WithError(err).WithField("StripeEventID", hookEvent.ID).Warn("could not get stripe event")
		return http.StatusInternalServerError, nil
	}
	if checkedEvent == nil {
		// I don't think this should occur
		logger.WithField("StripeEventID", hookEvent.ID).Error("could not verify stripe event, checked event is nil")
		return http.StatusForbidden, nil
	}

	log := logger.WithFields(logrus.Fields{
		"StripeEventType":   checkedEvent.Type,
		"StripeEventID":     checkedEvent.ID,
		"StripeEventLive":   checkedEvent.Live,
		"StripeEventReq":    checkedEvent.Req,
		"StripeEventUserID": checkedEvent.UserID,
	})

	switch checkedEvent.Type {
	case "customer.subscription.deleted":
		var sub stripe.Sub
		err := json.Unmarshal(checkedEvent.Data.Raw, &sub)
		if err != nil {
			return http.StatusBadRequest, errors.New("could not unmarshal subscription event")
		}
		log.WithField("StripeSubID", sub.ID).WithField("StripeCustomerID", sub.Customer.ID).Infof("subscription cancelled at %v", time.Unix(sub.PeriodEnd, 0))
	default:
		log.Info(checkedEvent.Data.Obj)
	}
	return http.StatusOK, nil
}

// logoutHandler logs a user out, if logged in, and redirects to the home page.
func logoutHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	session := session.FromContext(r.Context())
	if session.LoggedIn() {
		if err := session.Delete(w); err != nil {
			logger.WithError(err).Error("could not delete session")
		}
	}
	http.Redirect(w, r, "/", http.StatusFound)
	return 0, nil
}

// consoleIndexHandler displays the console's index page.
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
		logger.WithError(err).Error("error parsing console-index template")
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
		logger.WithError(err).Error("error parsing console-payments template")
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

	user.Logger.Infof("processed stripe subscription on plan %v", vars["planID"])

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

	user.Logger.Infof("cancelled stripe subscription subscriptionID %q", r.Form.Get("subscriptionID"))

	http.Redirect(w, r, "/console/payments", http.StatusFound)
	return 0, nil
}
