package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

	"github.com/bradleyfalzon/gopherci-web/internal/github"
	"github.com/bradleyfalzon/gopherci-web/internal/users"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	migrate "github.com/rubenv/sql-migrate"
)

var (
	db        *sql.DB            // db is sql db for persistent storage
	templates *template.Template // templates contains all the html templates
)

func main() {
	log.Println("Starting...")

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file:", err)
	}

	listen := os.Getenv("HTTP_LISTEN")

	log.Printf("Connecting to %q db name: %q, username: %q, host: %q, port: %q",
		os.Getenv("DB_DRIVER"), os.Getenv("DB_DATABASE"), os.Getenv("DB_USERNAME"), os.Getenv("DB_HOST"), os.Getenv("DB_PORT"),
	)

	dsn := fmt.Sprintf(`%s:%s@tcp(%s:%s)/%s?charset=utf8&collation=utf8_unicode_ci&timeout=6s&time_zone='%%2B00:00'&parseTime=true`,
		os.Getenv("DB_USERNAME"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_DATABASE"),
	)

	db, err = sql.Open(os.Getenv("DB_DRIVER"), dsn)
	if err != nil {
		log.Fatal("Error setting up DB:", err)
	}

	// Do DB migrations
	migrations := &migrate.FileMigrationSource{Dir: "migrations"}
	migrate.SetTable("migrations-web")
	direction := migrate.Up
	migrateMax := 0
	if len(os.Args) > 1 && os.Args[1] == "down" {
		direction = migrate.Down
		migrateMax = 1
	}
	n, err := migrate.ExecMax(db, os.Getenv("DB_DRIVER"), migrations, direction, migrateMax)
	log.Printf("Applied %d migrations to database", n)
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not execute all migrations"))
	}

	// Initialise html templates
	log.Println("Parsing templates...")
	if templates, err = template.ParseGlob("templates/*.tmpl"); err != nil {
		log.Fatalf("could not parse html templates: %s", err)
	}

	// UserManager
	um := users.NewUserManager(db)

	r := mux.NewRouter()
	r.NotFoundHandler = http.HandlerFunc(notFoundHandler)
	r.Handle("/", appHandler(homeHandler))
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// TODO panic handler?
	h := handlers.CombinedLoggingHandler(os.Stdout, r)
	h = handlers.CompressHandler(h)

	// Initialise GitHub
	switch {
	case os.Getenv("GITHUB_OAUTH_CLIENT_ID") == "":
		log.Fatal("GITHUB_OAUTH_CLIENT_ID is not set")
	case os.Getenv("GITHUB_OAUTH_CLIENT_SECRET") == "":
		log.Fatal("GITHUB_OAUTH_CLIENT_SECRET is not set")
	}
	gh := github.New(os.Getenv("GITHUB_OAUTH_CLIENT_ID"), os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"), um)
	r.Handle("/gh/login", appHandler(gh.OAuthLoginHandler))
	r.Handle("/gh/callback", appHandler(gh.OAuthCallbackHandler))

	log.Println("Listening on", listen)
	log.Fatal(http.ListenAndServe(listen, h))

}
