package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"

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

	// initialise html templates
	log.Println("Parsing templates...")
	if templates, err = template.ParseGlob("templates/*.tmpl"); err != nil {
		log.Fatalf("could not parse html templates: %s", err)
	}

	r := mux.NewRouter()
	r.NotFoundHandler = http.HandlerFunc(notFoundHandler)
	r.HandleFunc("/", homeHandler)
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// TODO panic handler?
	h := handlers.CombinedLoggingHandler(os.Stdout, r)
	h = handlers.CompressHandler(h)

	log.Println("Listening on", listen)
	log.Fatal(http.ListenAndServe(listen, h))

}

// homeHandler displays the home page
func homeHandler(w http.ResponseWriter, r *http.Request) {
	page := struct {
		Title string
	}{"GopherCI"}

	if err := templates.ExecuteTemplate(w, "home.tmpl", page); err != nil {
		log.Println("error parsing home template:", err)
	}
}

// notFoundHandler displays a 404 not found error
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	errorHandler(w, r, http.StatusNotFound, "")
}

// errorHandler handles an error message, with an optional description
func errorHandler(w http.ResponseWriter, r *http.Request, code int, desc string) {
	page := struct {
		Title  string
		Code   string // eg 400
		Status string // eg Bad Request
		Desc   string // eg Missing key foo
	}{fmt.Sprintf("%d - %s", code, http.StatusText(code)), strconv.Itoa(code), http.StatusText(code), desc}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	if err := templates.ExecuteTemplate(w, "error.tmpl", page); err != nil {
		log.Println("error parsing error template:", err)
	}
}
