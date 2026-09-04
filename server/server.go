package server

import (
	"fiberseed/database"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/unrolled/secure"
)

func setupMiddlewares(app *chi.Mux) {
	// The original settled the routing path once, before anything else ran, so
	// the lookups the rest of the stack performs see the same path the route
	// table is matched against.
	app.Use(nonStrictRouting)
	app.Use(middleware.GetHead)
	app.Use(secure.New(secure.Options{
		BrowserXssFilter:        true,
		ContentTypeNosniff:      true,
		CustomFrameOptionsValue: "SAMEORIGIN",
	}).Handler)
	app.Use(recoverer)
	app.Use(cors)
	app.Use(compress)
	app.Use(etag)
	if os.Getenv("ENABLE_LIMITER") != "" {
		app.Use(httprate.LimitByIP(5, time.Minute))
	}
	if os.Getenv("ENABLE_LOGGER") != "" {
		app.Use(middleware.Logger)
	}
}

func Create() *chi.Mux {
	database.SetupDatabase()

	app := chi.NewRouter()

	setupMiddlewares(app)

	app.Get("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})

	return app
}

func Listen(app *chi.Mux) error {

	// 404 Handler
	notFound := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(http.StatusText(http.StatusNotFound)))
	}
	app.NotFound(notFound)
	app.MethodNotAllowed(notFound)

	serverHost := os.Getenv("SERVER_HOST")
	serverPort := os.Getenv("SERVER_PORT")

	return http.ListenAndServe(fmt.Sprintf("%s:%s", serverHost, serverPort), app)
}
