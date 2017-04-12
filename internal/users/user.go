package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/Sirupsen/logrus"
	"github.com/google/go-github/github"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/coupon"
	"github.com/stripe/stripe-go/customer"
	"github.com/stripe/stripe-go/invoice"
	"github.com/stripe/stripe-go/sub"
)

// User represents a GopherCI-web user.
type User struct {
	Logger           *logrus.Entry
	db               *sqlx.DB
	GHClient         *github.Client
	UserID           int    `db:"id"`
	Email            string `db:"email"`
	GitHubID         int    `db:"github_id"`
	GitHubToken      []byte `db:"github_token"` // nil if none assigned to user
	StripeCustomerID string `db:"stripe_customer_id"`
}

// GetUser looks up a user in the db and returns it, if no user was found,
// user is nil, if an error occurs it will be returned.
func GetUser(logger *logrus.Entry, db *sqlx.DB, oauthConf *oauth2.Config, userID int) (*User, error) {
	user := &User{db: db}
	err := db.Get(user, "SELECT id, email, github_id, github_token, stripe_customer_id FROM users WHERE id = ?", userID)
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, errors.Wrap(err, "could not select from users")
	}
	// GitHubToken is nil if no tokens are assigned to the user, in that case
	// we'll provide an empty token to GHClient let it handle the inevitable
	// authentication error. The callers can handle that error themselves (such
	// as reestablishing the oauth flow).
	var token oauth2.Token
	if user.GitHubToken != nil {
		if err := json.Unmarshal(user.GitHubToken, &token); err != nil {
			return nil, err
		}
		user.GitHubToken = nil
	}
	user.GHClient = NewClient(oauthConf, &token)
	user.Logger = logger.WithField("userID", user.UserID)
	return user, nil
}

// GitHubListOrgMembershipsActive returns active
// https://godoc.org/github.com/google/go-github/github#OrganizationsService.ListOrgMemberships
func (u *User) GitHubListOrgMembershipsActive(ctx context.Context) ([]*github.Membership, error) {
	memberships, _, err := u.GHClient.Organizations.ListOrgMemberships(ctx, &github.ListOrgMembershipsOptions{State: "active"})
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

func (u *User) ProcessStripePayment(token, plan string) error {
	// 2017-01-22, we've switched from AUD to USD currency in stripe, so existing
	// customers need a new stripe customer ID as stripe won't accept a single
	// customer with multiple currencies. So create a new stripe customer for
	// these users. We can remove the "u.UserID > 17"  condition once everyone is
	// off of the PersonalMonthly plan. See commit message for more information.
	if u.StripeCustomerID != "" && u.UserID > 17 {
		// TODO this should upgrade the existing plan (prorata) #8
		_, err := sub.New(&stripe.SubParams{
			Customer: u.StripeCustomerID,
			Plan:     plan,
		})
		if err != nil {
			return errors.Wrapf(err, "could not subscribe userID %v stripe customer %v to %q", u.UserID, u.StripeCustomerID, plan)
		}
		return nil
	}

	customerParams := &stripe.CustomerParams{
		Plan: plan,
		Params: stripe.Params{
			Meta: map[string]string{"userID": strconv.FormatInt(int64(u.UserID), 10)},
		},
	}
	_ = customerParams.SetSource(token)
	customer, err := customer.New(customerParams)
	if err != nil {
		return errors.Wrap(err, "could not create stripe customer")
	}

	_, err = u.db.Exec(`UPDATE users SET stripe_customer_id = ? WHERE ID = ?`, customer.ID, u.UserID)
	if err != nil {
		return errors.Wrapf(err, "Created stripe customer with id %q but could not allocate to userID %v", customer.ID, u.UserID)
	}
	return nil
}

// StripeCustomer gets the stripe customer, returns nil if there's no stripe
// customer ID or an error if an error occurs.
func (u *User) StripeCustomer() (*stripe.Customer, error) {
	if u.StripeCustomerID == "" {
		return nil, nil
	}
	customer, err := customer.Get(u.StripeCustomerID, nil)
	return customer, errors.Wrapf(err, "could not get stripe customer id %q", u.StripeCustomerID)
}

// ProcessStripeCoupon adds a couponID to a stripe customer.
func (u *User) ProcessStripeCoupon(couponID string) error {
	coupon, err := coupon.Get(couponID, nil)
	if err != nil {
		return err
	}
	if !coupon.Valid {
		return errors.New("coupon does not exist")
	}

	customerParams := &stripe.CustomerParams{
		Coupon: couponID,
	}
	_, err = customer.Update(u.StripeCustomerID, customerParams)
	return err
}

// Invoice represents an upcoming or previous invoice.
type Invoice struct {
	AmountDisplay string
	DueDate       time.Time
}

// StripeUpcomingInvoice returns the upcoming invoice for a user, nil if
// there are no upcoming invoices for this user, or an error.
func (u *User) StripeUpcomingInvoice() (*Invoice, error) {
	if u.StripeCustomerID == "" {
		return nil, nil
	}
	invoice, err := invoice.GetNext(&stripe.InvoiceParams{Customer: u.StripeCustomerID})
	if err != nil {
		return nil, err
	}

	return &Invoice{
		AmountDisplay: amountString(invoice.Currency, invoice.Amount),
		DueDate:       time.Unix(invoice.Date, 0),
	}, nil
}

// Subscription represents a payment subscription.
type Subscription struct {
	ID            string
	Name          string    // Name is the plan name.
	AmountDisplay string    // AmountDisplay is the amount formatted for display.
	AmountCents   uint      // AmountCents is the amount in cents.
	Interval      string    // Interval is the billing interval, such as month.
	StartedAt     time.Time // StartedAt is the date started.
	PeriodEndAt   time.Time // PeriodEndAt is the end date of the current interval.
	CancelledAt   time.Time // CancelledAt is the date the subscription was cancelled.
	// EndedAt is the date the subscription finally ended (it may have been
	// cancelled and ended at the period end).
	EndedAt time.Time
	// Ended is whether the subcription is currently active (it maybe cancelled,
	// but not currently ended).
	Ended bool
}

// Discount represents a discount to be applied.
type Discount struct {
	CouponID    string
	StartedAt   time.Time
	EndedAt     time.Time
	Description string
}

// StripeDiscount returns the discount for the current user. If the user does
// not have an active discount, nil is returned.
func (u *User) StripeDiscount(customer *stripe.Customer) *Discount {
	if customer.Discount == nil {
		return nil
	}

	discount := Discount{
		CouponID:  customer.Discount.Coupon.ID,
		StartedAt: time.Unix(customer.Discount.Start, 0),
		EndedAt:   time.Unix(customer.Discount.End, 0),
	}

	if customer.Discount.Deleted || time.Now().After(discount.EndedAt) {
		return nil
	}

	switch {
	case customer.Discount.Coupon.Percent > 0:
		discount.Description = fmt.Sprintf("%d%% off", customer.Discount.Coupon.Percent)
	case customer.Discount.Coupon.Amount > 0:
		discount.Description = fmt.Sprintf("%d%% off", customer.Discount.Coupon.Percent)
	}

	switch customer.Discount.Coupon.Duration {
	case coupon.Forever:
		discount.Description += " forever"
	case coupon.Once:
		discount.Description += " the next invoice"
	case coupon.Repeating:
		discount.Description += fmt.Sprintf(" for %d months", customer.Discount.Coupon.DurationPeriod)
	}

	return &discount
}

// StripeSubscriptions returns a slice of subscriptions for the current user,
// both current and previous subscriptions are returned.
func (u *User) StripeSubscriptions(customer *stripe.Customer) []Subscription {
	var subs []Subscription
	if customer.Subs == nil {
		return nil
	}
	for _, sub := range customer.Subs.Values {
		s := Subscription{
			ID:            sub.ID,
			Name:          sub.Plan.Name,
			AmountDisplay: amountString(sub.Plan.Currency, int64(sub.Plan.Amount)),
			AmountCents:   uint(sub.Plan.Amount),
			Interval:      string(sub.Plan.Interval),
			Ended:         sub.EndCancel,
		}
		if sub.Start > 0 {
			s.StartedAt = time.Unix(sub.Start, 0)
		}
		if sub.PeriodEnd > 0 {
			s.PeriodEndAt = time.Unix(sub.PeriodEnd, 0)
		}
		if sub.Canceled > 0 {
			s.CancelledAt = time.Unix(sub.Canceled, 0)
		}
		if sub.Ended > 0 {
			s.EndedAt = time.Unix(sub.Ended, 0)
		}
		subs = append(subs, s)
	}
	return subs
}

func amountString(currency stripe.Currency, amount int64) string {
	return fmt.Sprintf("$%s %.2f", strings.ToUpper(string(currency)), float64(amount)/100)
}

// CancelStripeSubscription cancels a stripe subscription at the end of the
// current billing period if endCancel is true. It does not disable any
// enabled installations.
func (u *User) CancelStripeSubscription(id string, endCancel bool) error {
	_, err := sub.Cancel(id, &stripe.SubParams{EndCancel: endCancel})
	return err
}
