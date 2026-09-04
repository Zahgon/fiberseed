package pkg

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestError(t *testing.T) {
	err := EntityNotFound("No book found")
	assert.Equal(t, "No book found", err.Error())
	assert.Equal(t, 404, err.Status)
	assert.Equal(t, "entity-not-found", err.Code)
}

func TestBadRequest(t *testing.T) {
	err := BadRequest("Invalid params")
	assert.Equal(t, 400, err.Status)
	assert.Equal(t, "bad-request", err.Code)
	assert.Equal(t, "Invalid params", err.Message)
}

func TestUnexpected(t *testing.T) {
	err := Unexpected("boom")
	assert.Equal(t, 500, err.Status)
	assert.Equal(t, "internal-server", err.Code)
	assert.Equal(t, "boom", err.Message)
}

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	err := JSON(rec, 200, map[string]string{"hello": "world"})

	assert.Equal(t, nil, err)
	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"hello":"world"}`, rec.Body.String())
}

func TestJSONUnserializableValue(t *testing.T) {
	rec := httptest.NewRecorder()
	err := JSON(rec, 200, make(chan int))

	assert.NotEqual(t, nil, err)
	assert.Equal(t, 500, err.(*Error).Status)
	assert.Equal(t, "internal-server", err.(*Error).Code)
	assert.Equal(t, "", rec.Body.String())
	assert.Equal(t, "", rec.Header().Get("Content-Type"))
}

func TestStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	err := Status(rec, 204)

	assert.Equal(t, nil, err)
	assert.Equal(t, 204, rec.Code)
	assert.Equal(t, "", rec.Body.String())
}

func TestWriteErrorWithKnownError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, EntityNotFound("No book found"))

	assert.Equal(t, 404, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"status":404,"code":"entity-not-found","message":"No book found"}`, rec.Body.String())
}

func TestWriteErrorWithUnknownError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, errors.New("something went wrong"))

	assert.Equal(t, 500, rec.Code)
	assert.Equal(t, `{"status":500,"code":"internal-server","message":"something went wrong"}`, rec.Body.String())
}

func TestHandlerServeHTTPWithoutError(t *testing.T) {
	rec := httptest.NewRecorder()
	handler := Handler(func(w http.ResponseWriter, r *http.Request) error {
		return JSON(w, 200, map[string]string{"ok": "true"})
	})

	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, `{"ok":"true"}`, rec.Body.String())
}

func TestHandlerServeHTTPWithError(t *testing.T) {
	rec := httptest.NewRecorder()
	handler := Handler(func(w http.ResponseWriter, r *http.Request) error {
		return BadRequest("Invalid params")
	})

	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, 400, rec.Code)
	assert.Equal(t, `{"status":400,"code":"bad-request","message":"Invalid params"}`, rec.Body.String())
}
