package books

import (
	"fiberseed/pkg"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func Routes(route chi.Router) {
	route.Method(http.MethodGet, "/books", pkg.Handler(GetBooks))
	route.Method(http.MethodGet, "/books/{id}", pkg.Handler(GetBook))
	route.Method(http.MethodPut, "/books/{id}", pkg.Handler(UpdateBook))
	route.Method(http.MethodPost, "/books", pkg.Handler(NewBook))
	route.Method(http.MethodDelete, "/books/{id}", pkg.Handler(DeleteBook))
}
