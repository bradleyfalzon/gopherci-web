package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bradleyfalzon/gopherci-web/internal/session"
	"github.com/bradleyfalzon/gopherci-web/internal/users"
	"github.com/google/go-github/github"
	"github.com/pressly/chi"
	"github.com/sirupsen/logrus"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/event"
)

func SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := session.GetOrCreate(db, w, r)
		if err != nil {
			logger.WithError(err).Error("could not get session")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		ctx := context.WithValue(r.Context(), session.CtxKey{}, s)
		next.ServeHTTP(w, r.WithContext(ctx))

		if err := s.Save(); err != nil {
			logger.WithError(err).Error("could not save session")
		}
	})
}

type userCtxKey struct{}

func MustBeUserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := session.FromContext(r.Context())
		if !sess.LoggedIn() {
			http.Redirect(w, r, "/gh/login", http.StatusFound)
			return
		}
		user, err := um.GetUser(sess.UserID)
		if err != nil {
			logger.WithError(err).Error("could not get user")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if user == nil {
			http.Redirect(w, r, "/gh/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// homeHandler displays the home page
func homeHandler(w http.ResponseWriter, _ *http.Request) {
	page := struct {
		Title string
	}{"GopherCI"}

	if err := templates.ExecuteTemplate(w, "home.tmpl", page); err != nil {
		logger.WithError(err).Error("error parsing home template")
	}
}

// notFoundHandler displays a 404 not found error
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	errorHandler(w, r, http.StatusNotFound, fmt.Sprintf("%q not found", r.URL))
}

// errorHandler handles an error message, with an optional description
func errorHandler(w http.ResponseWriter, _ *http.Request, code int, desc string) {
	page := struct {
		Title  string
		Code   string // eg 400
		Status string // eg Bad Request
		Desc   string // eg Missing key foo
	}{fmt.Sprintf("%d - %s", code, http.StatusText(code)), strconv.Itoa(code), http.StatusText(code), desc}

	if page.Desc == "" {
		page.Desc = http.StatusText(code)
	}

	// TODO check accept header and respond in json if accepted instead of html

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	if err := templates.ExecuteTemplate(w, "error.tmpl", page); err != nil {
		logger.WithError(err).Error("error parsing error template")
	}
}

// stripeEventHandler handles stripe webhooks/events.
func stripeEventHandler(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(r.Body)
	var hookEvent stripe.Event
	if err := dec.Decode(&hookEvent); err != nil {
		errorHandler(w, r, http.StatusBadRequest, "could not decode stripe event")
		return
	}

	// Check authenticity with stripe
	checkedEvent, err := event.Get(hookEvent.ID, nil)
	if err != nil {
		// client error or event doesn't exist
		if serr, ok := err.(*stripe.Error); ok && serr.HTTPStatusCode == http.StatusNotFound {
			errorHandler(w, r, http.StatusForbidden, "")
			return
		}
		logger.WithError(err).WithField("StripeEventID", hookEvent.ID).Warn("could not get stripe event")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	}
	if checkedEvent == nil {
		// I don't think this should occur
		logger.WithField("StripeEventID", hookEvent.ID).Error("could not verify stripe event, checked event is nil")
		errorHandler(w, r, http.StatusForbidden, "")
		return
	}

	log := logger.WithFields(logrus.Fields{
		"StripeEventType":   checkedEvent.Type,
		"StripeEventID":     checkedEvent.ID,
		"StripeEventLive":   checkedEvent.Livemode,
		"StripeEventReq":    checkedEvent.Request,
		"StripeEventUserID": checkedEvent.UserID,
	})

	switch checkedEvent.Type {
	case "customer.subscription.deleted":
		var sub stripe.Subscription
		err := json.Unmarshal(checkedEvent.Data.Raw, &sub)
		if err != nil {
			errorHandler(w, r, http.StatusBadRequest, "could not unmarshal subscription event")
			return
		}
		log.WithField("StripeSubID", sub.ID).WithField("StripeCustomerID", sub.Customer.ID).Infof("subscription cancelled at %v", time.Unix(sub.CurrentPeriodEnd, 0))
	default:
		log.Info(checkedEvent.Data.Object)
	}
}

// logoutHandler logs a user out, if logged in, and redirects to the home page.
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess.LoggedIn() {
		if err := sess.Delete(w); err != nil {
			logger.WithError(err).Error("could not delete sess")
		}
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// consoleIndexHandler displays the console's index page.
func consoleIndexHandler(w http.ResponseWriter, r *http.Request) {
	type install struct {
		AccountID      int64 // github accountID, may not be set on orphaned installations
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
		NewCustomer     bool
	}{Title: "Console"}

	// Check if logged in
	// TODO this should be a part of middleware
	sess := session.FromContext(r.Context())
	if !sess.LoggedIn() {
		http.Redirect(w, r, "/gh/login", http.StatusFound)
		return
	}

	user := r.Context().Value(userCtxKey{}).(*users.User)
	page.Email = user.Email

	// New customer, show them a welcome message
	if r.FormValue("success") != "" {
		page.NewCustomer = true
	}

	ghUser, _, err := user.GHClient.Users.Get(r.Context(), "")
	if err != nil {
		logger.WithError(err).Info("could not get github user, reattempting oauth flow")
		http.Redirect(w, r, "/gh/login", http.StatusFound)
		return
	}

	// Get list of organisations from github api for this user
	ghMemberships, _, err := user.GHClient.Organizations.ListOrgMemberships(r.Context(), &github.ListOrgMembershipsOptions{State: "active"})
	if err != nil {
		logger.WithError(err).Info("could not get github list org memberships, reattempting oauth flow")
		http.Redirect(w, r, "/gh/login", http.StatusFound)
		return
	}

	ei, err := user.EnabledInstallations()
	if err != nil {
		logger.WithError(err).Error("could not get enabled installations")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	}
	enabledInstallations := make(map[int]bool)
	for _, installationID := range ei {
		enabledInstallations[installationID] = true
	}

	// Refactor the following into gciweb package? Yes

	accountIDs := []int64{*ghUser.ID}
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
		logger.WithError(err).Error("could not list installations")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
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

	customer, err := user.StripeCustomer()
	switch {
	case err != nil:
		user.Logger.WithError(err).Error("could not get stripe customer")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	case customer != nil:
		subs := user.StripeSubscriptions(customer)
		for _, sub := range subs {
			if sub.CancelledAt.IsZero() {
				page.HasSubscription = true
			}
		}
	}

	if err := templates.ExecuteTemplate(w, "console-index.tmpl", page); err != nil {
		logger.WithError(err).Error("error parsing console-index template")
	}
}

// consoleInstallStateHandler enables or disabled an installation
func consoleInstallStateHandler(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userCtxKey{}).(*users.User)

	i, err := strconv.ParseInt(r.FormValue("installationID"), 10, 64)
	if err != nil {
		errorHandler(w, r, http.StatusBadRequest, "Invalid installationID")
		return
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
			errorHandler(w, r, http.StatusForbidden, "Installation not enabled for this user")
			return
		}
		err = user.DisableInstallation(installationID)
		if err == nil {
			err = gciClient.DisableInstallation(installationID)
		}
	default:
		errorHandler(w, r, http.StatusBadRequest, "Invalid state")
		return
	}
	if err != nil {
		logger.WithError(err).Error("could not change installation state")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	}

	http.Redirect(w, r, "/console", http.StatusFound)
}

// consoleBillingHandler manages plans.
func consoleBillingHandler(w http.ResponseWriter, r *http.Request) {
	page := struct {
		Title            string
		Email            string
		StripePublishKey string
		Subscriptions    []users.Subscription
		HasSubscription  bool
		IsStripeCustomer bool
		UpcomingInvoice  *users.Invoice
		Discount         *users.Discount
	}{Title: "Billing", StripePublishKey: os.Getenv("STRIPE_PUBLISH_KEY")}

	user := r.Context().Value(userCtxKey{}).(*users.User)
	page.Email = user.Email

	customer, err := user.StripeCustomer()
	switch {
	case err != nil:
		user.Logger.WithError(err).Error("could not get stripe customer")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	case customer != nil:
		page.IsStripeCustomer = true
		page.Discount = user.StripeDiscount(customer)
		page.Subscriptions = user.StripeSubscriptions(customer)
		for _, sub := range page.Subscriptions {
			if sub.CancelledAt.IsZero() {
				page.HasSubscription = true
			}
		}
	}

	page.UpcomingInvoice, err = user.StripeUpcomingInvoice()
	if err != nil {
		user.Logger.WithError(err).Error("could not get upcoming invoice for customer")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	}

	if err := templates.ExecuteTemplate(w, "console-billing.tmpl", page); err != nil {
		logger.WithError(err).Error("error parsing console-billing template")
	}
}

// consoleBillingProcessHandler processes the results of a payment (not the
// payment itself).
func consoleBillingProcessHandler(w http.ResponseWriter, r *http.Request) {
	var (
		planID = chi.URLParam(r, "planID")
		user   = r.Context().Value(userCtxKey{}).(*users.User)
	)

	customer, err := user.StripeCustomer()
	switch {
	case err != nil:
		user.Logger.WithError(err).Error("could not get stripe customer")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	case customer != nil:
		// Ensure customer does not have any current subscriptions until we have the
		// ability to prorata subscriptions.
		subs := user.StripeSubscriptions(customer)
		for _, sub := range subs {
			if sub.CancelledAt.IsZero() {
				// TODO flash message
				errorHandler(w, r, http.StatusBadRequest, "active subscription already exists")
				return
			}
		}
	}

	err = user.ProcessStripePayment(r.FormValue("stripeToken"), planID)
	if err != nil {
		logger.WithError(err).Error("could not process stripe payment")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	}

	user.Logger.Infof("processed stripe subscription on plan %q", planID)

	http.Redirect(w, r, "/console?success=1", http.StatusFound)
}

// consoleBillingCancelHandler cancels a subscription.
func consoleBillingCancelHandler(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(userCtxKey{}).(*users.User)

	customer, err := user.StripeCustomer()
	switch {
	case err != nil:
		user.Logger.WithError(err).Error("could not get stripe customer")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	case customer == nil:
		errorHandler(w, r, http.StatusBadRequest, "Not a stripe customer")
		return
	}

	subs := user.StripeSubscriptions(customer)
	var sub *users.Subscription
	for _, s := range subs {
		if s.ID == r.FormValue("subscriptionID") {
			sub = &s
			break
		}
	}
	if sub == nil {
		// TODO flash message to show user we couldn't find subscription ID
		errorHandler(w, r, http.StatusBadRequest, "could not find subscription ID")
		return
	}

	err = user.CancelStripeSubscription(r.Form.Get("subscriptionID"), true)
	if err != nil {
		logger.WithError(err).Error("could not process stripe payment")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	}

	// TODO disable all integrations at sub.PeriodEnd #7

	user.Logger.Infof("cancelled stripe subscription subscriptionID %q", r.Form.Get("subscriptionID"))

	http.Redirect(w, r, "/console/billing", http.StatusFound)
}

// consoleBillingCouponHandler adds coupons to an account.
func consoleBillingCouponHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var (
		couponID = r.Form.Get("couponID")
		user     = r.Context().Value(userCtxKey{}).(*users.User)
	)

	customer, err := user.StripeCustomer()
	switch {
	case err != nil:
		user.Logger.WithError(err).Error("could not get stripe customer")
		errorHandler(w, r, http.StatusInternalServerError, "")
		return
	case customer == nil:
		errorHandler(w, r, http.StatusBadRequest, "Not a stripe customer")
		return
	}

	if user.StripeDiscount(customer) != nil {
		errorHandler(w, r, http.StatusBadRequest, "Existing discount already exists")
		return
	}

	err = user.ProcessStripeCoupon(couponID)
	if err != nil {
		user.Logger.WithError(err).Error("could not process/apply stripe coupon")
		errorHandler(w, r, http.StatusBadRequest, "Cannot apply coupon")
		return
	}

	user.Logger.Infof("processed stripe coupon %v", couponID)

	http.Redirect(w, r, "/console/billing", http.StatusFound)
}
