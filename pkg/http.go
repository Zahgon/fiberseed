package pkg

import (
	"encoding/json"
	"net/http"
)

// MIMEApplicationJSON is the content type sent with every JSON response.
const MIMEApplicationJSON = "application/json"

// Handler is an HTTP handler that is allowed to fail. Returning an error hands
// the response over to WriteError so error rendering stays in one place instead
// of being repeated in every handler.
type Handler func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP lets a Handler be mounted anywhere an http.Handler is expected.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, err)
	}
}

// JSON serializes v and writes it with the given status code.
func JSON(w http.ResponseWriter, status int, v interface{}) error {
	body, err := json.Marshal(v)
	if err != nil {
		return Unexpected(err.Error())
	}

	w.Header().Set("Content-Type", MIMEApplicationJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)

	return nil
}

// Status writes a status code with an empty response body.
func Status(w http.ResponseWriter, status int) error {
	w.WriteHeader(status)
	return nil
}

// WriteError renders err as a JSON *Error. Anything that is not already an
// *Error is reported as an unexpected internal failure.
func WriteError(w http.ResponseWriter, err error) {
	e, ok := err.(*Error)
	if !ok {
		e = Unexpected(err.Error())
	}

	_ = JSON(w, e.Status, e)
}
