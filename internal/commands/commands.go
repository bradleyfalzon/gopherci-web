package commands

import (
	"database/sql"

	"github.com/pkg/errors"
	"github.com/rubenv/sql-migrate"
	"github.com/sirupsen/logrus"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/customer"
)

// Command represents a single command to be executed and then for the process
// to end.
type Command struct {
	logger *logrus.Logger
}

// NewCommand returns a Command with db and logger attached.
func NewCommand() *Command {
	logger := logrus.New()
	logger.Level = logrus.WarnLevel
	return &Command{logger: logger}
}

// Migrate migrates the database using migrations in migrations/ directory. If
// direction is up, no limit is applied to the number of migrations, if
// direction is down, only 1 down migration is executed.
func (c *Command) Migrate(db *sql.DB, driver string, direction migrate.MigrationDirection) {
	migrations := &migrate.FileMigrationSource{Dir: "migrations"}
	migrate.SetTable("migrations-web") // Legacy, no reason we can't move back to default name
	migrateMax := 0
	if direction == migrate.Down {
		migrateMax = 1
	}
	n, err := migrate.ExecMax(db, driver, migrations, direction, migrateMax)
	c.logger.Printf("Applied %d migrations to database", n)
	if err != nil {
		c.logger.Fatal(errors.Wrap(err, "could not execute all migrations"))
	}
}

// BillingCheck checks stripe billing for discrepancies.
func (c *Command) BillingCheck(stripeSecretKey string) {
	stripe.Key = stripeSecretKey
	stripe.LogLevel = 1

	// Find single stripe customers with multiple subscriptions
	{
		customers := customer.List(nil)
		for customers.Next() {
			thisCustomer := customers.Customer()

			var hasValidSubscription bool
			for _, sub := range thisCustomer.Subscriptions.Data {
				if sub.EndCancel {
					// not active
					continue
				}
				// active
				if hasValidSubscription {
					c.logger.Warnf("customer %q has multiple valid subscriptions", thisCustomer.ID)
					break
				}
				hasValidSubscription = true
			}
		}
		if err := customers.Err(); err != nil {
			c.logger.WithError(err).Fatal("could not get customer list for multiple subscribers check")
		}
	}

	// Find different stripe customers with same userID which also have active subscriptions
	{
		seenUserIDs := make(map[string]string) // userID => stripeCustomerID

		customers := customer.List(nil)
		for customers.Next() {
			thisCustomer := customers.Customer()

			var hasValidSubscription bool
			for _, sub := range thisCustomer.Subscriptions.Data {
				if !sub.EndCancel {
					hasValidSubscription = true
				}
			}
			if !hasValidSubscription {
				continue
			}

			if seenCustomerID := seenUserIDs[thisCustomer.Metadata["userID"]]; seenCustomerID != "" {
				c.logger.Warnf("userID %q has customerID %q and %q", thisCustomer.Metadata["userID"], seenCustomerID, thisCustomer.ID)
			}
			seenUserIDs[thisCustomer.Metadata["userID"]] = thisCustomer.ID
		}
		if err := customers.Err(); err != nil {
			c.logger.WithError(err).Fatal("could not get customer list for multiple customers same userID check")
		}
	}
}
