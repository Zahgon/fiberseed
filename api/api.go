package api

import (
	"fiberseed/api/books"

	"github.com/go-chi/chi/v5"
)

func Setup(app chi.Router) {
	app.Route("/api/v1", func(v1 chi.Router) {
		books.Routes(v1)
	})
}
