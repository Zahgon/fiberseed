package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zlib"
	"github.com/stretchr/testify/assert"
)

// okHandler writes a plain 200 response with a body, which is the only shape
// the etag middleware is supposed to act on.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("OK"))
	})
}

func TestETagAddsValidatorToSuccessfulResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	etag(okHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
	assert.NotEqual(t, "", rec.Header().Get("ETag"))
}

func TestETagReturnsNotModifiedWhenClientHoldsEntity(t *testing.T) {
	first := httptest.NewRecorder()
	etag(okHandler()).ServeHTTP(first, httptest.NewRequest("GET", "/", nil))
	tag := first.Header().Get("ETag")

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", tag)

	second := httptest.NewRecorder()
	etag(okHandler()).ServeHTTP(second, req)

	assert.Equal(t, 304, second.Code)
	assert.Equal(t, "", second.Body.String())
	assert.Equal(t, "", second.Header().Get("ETag"))
	assert.Equal(t, "", second.Header().Get("Content-Type"))
	assert.Equal(t, "", second.Header().Get("Content-Length"))
}

func TestETagSkipsResponsesWithoutStatusOK(t *testing.T) {
	rec := httptest.NewRecorder()
	etag(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})).ServeHTTP(rec, httptest.NewRequest("DELETE", "/", nil))

	assert.Equal(t, 204, rec.Code)
	assert.Equal(t, "", rec.Header().Get("ETag"))
	assert.Equal(t, "", rec.Body.String())
}

func TestETagSkipsEmptyBodies(t *testing.T) {
	rec := httptest.NewRecorder()
	etag(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "", rec.Header().Get("ETag"))
}

func TestMatchETag(t *testing.T) {
	tag := `"2-3212384700"`

	cases := []struct {
		name     string
		header   string
		expected bool
	}{
		{"empty header", "", false},
		{"blank header", "   ", false},
		{"wildcard is not a match", "*", false},
		{"exact match", tag, true},
		{"weak match", `W/` + tag, true},
		{"weak mismatch", `W/"1-1"`, false},
		{"weak header never falls back to containment", `W/"other", ` + tag, false},
		{"match in list", `"other", ` + tag, true},
		{"match embedded in a larger token", "x" + tag + "y", true},
		{"unquoted validator", `2-3212384700`, false},
		{"no match", `"1-1"`, false},
	}

	for _, c := range cases {
		assert.Equal(t, c.expected, matchETag(c.header, tag), c.name)
	}
}

func TestRecovererRendersPanicAsJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, 500, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"status":500,"code":"internal-server","message":"boom"}`, rec.Body.String())
}

func TestRecovererPropagatesAbortHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	handler := recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	assert.Panics(t, func() {
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	})
}

func TestNonStrictRouting(t *testing.T) {
	router := chi.NewRouter()
	router.Use(nonStrictRouting)
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("root"))
	})
	router.Get("/books", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("list"))
	})
	router.Get("/books/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chi.URLParam(r, "id")))
	})

	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/", http.StatusOK, "root"},
		{"/books", http.StatusOK, "list"},
		{"/books/", http.StatusOK, "list"},
		{"/books//", http.StatusOK, "list"},
		{"/books/7", http.StatusOK, "7"},
		{"/books/7/", http.StatusOK, "7"},
		{"/books/7//", http.StatusOK, "7"},
		{"//", http.StatusNotFound, ""},
		{"///", http.StatusNotFound, ""},
		{"/unknown/", http.StatusNotFound, ""},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))

		assert.Equal(t, c.status, rec.Code, c.path)
		if c.body != "" {
			assert.Equal(t, c.body, rec.Body.String(), c.path)
		}
	}
}

func TestNonStrictRoutingIgnoresLetterCase(t *testing.T) {
	router := chi.NewRouter()
	router.Use(nonStrictRouting)
	router.Get("/books", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("list"))
	})
	router.Get("/books/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chi.URLParam(r, "id")))
	})

	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/BOOKS", http.StatusOK, "list"},
		{"/Books", http.StatusOK, "list"},
		{"/BOOKS/", http.StatusOK, "list"},
		{"/BOOKS/7", http.StatusOK, "7"},
		{"/UNKNOWN", http.StatusNotFound, ""},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))

		assert.Equal(t, c.status, rec.Code, c.path)
		if c.body != "" {
			assert.Equal(t, c.body, rec.Body.String(), c.path)
		}
	}
}

func TestRouteExists(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/books", func(w http.ResponseWriter, r *http.Request) {})

	rctx := chi.NewRouteContext()
	rctx.Routes = router

	assert.True(t, routeExists(rctx, http.MethodGet, "/books"))
	assert.True(t, routeExists(rctx, http.MethodHead, "/books"), "HEAD is answered from the GET route")
	assert.False(t, routeExists(rctx, http.MethodPost, "/books"))
	assert.False(t, routeExists(rctx, http.MethodGet, "/missing"))
	assert.False(t, routeExists(chi.NewRouteContext(), http.MethodGet, "/books"), "no route table to consult")
}

// compressibleBody is long enough to be worth encoding and repetitive enough
// that every encoder shortens it.
var compressibleBody = strings.Repeat("fiberseed migration payload ", 20)

func compressedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(compressibleBody))
	})
}

func TestCompressEncodesLargeBodies(t *testing.T) {
	cases := []struct {
		accept   string
		encoding string
		decode   func(t *testing.T, encoded []byte) string
	}{
		{"br", "br", func(t *testing.T, encoded []byte) string {
			decoded, err := io.ReadAll(brotli.NewReader(strings.NewReader(string(encoded))))
			assert.NoError(t, err)

			return string(decoded)
		}},
		{"gzip", "gzip", func(t *testing.T, encoded []byte) string {
			zr, err := gzip.NewReader(strings.NewReader(string(encoded)))
			assert.NoError(t, err)

			decoded, err := io.ReadAll(zr)
			assert.NoError(t, err)

			return string(decoded)
		}},
		{"deflate", "deflate", func(t *testing.T, encoded []byte) string {
			zr, err := zlib.NewReader(strings.NewReader(string(encoded)))
			assert.NoError(t, err)

			decoded, err := io.ReadAll(zr)
			assert.NoError(t, err)

			return string(decoded)
		}},
	}

	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Encoding", c.accept)

		rec := httptest.NewRecorder()
		compress(compressedHandler()).ServeHTTP(rec, req)

		encoded := rec.Body.Bytes()

		assert.Equal(t, 200, rec.Code, c.accept)
		assert.Equal(t, c.encoding, rec.Header().Get("Content-Encoding"), c.accept)
		assert.Equal(t, strconv.Itoa(len(encoded)), rec.Header().Get("Content-Length"), c.accept)
		assert.Less(t, len(encoded), len(compressibleBody), c.accept)
		assert.Equal(t, compressibleBody, c.decode(t, encoded), c.accept)
		assert.Equal(t, "", rec.Header().Get("Vary"), c.accept)
	}
}

func TestCompressPreferredEncodingOrder(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")

	rec := httptest.NewRecorder()
	compress(compressedHandler()).ServeHTTP(rec, req)

	assert.Equal(t, "br", rec.Header().Get("Content-Encoding"), "br is offered ahead of gzip and deflate")
}

func TestCompressLeavesResponseAlone(t *testing.T) {
	cases := []struct {
		name        string
		accept      string
		contentType string
		encoding    string
		body        string
	}{
		{"body below the minimum size", "gzip", "application/json", "", "short"},
		{"payload that is not text", "gzip", "image/png", "", compressibleBody},
		{"already encoded", "gzip", "application/json", "gzip", compressibleBody},
		{"client accepts nothing", "", "application/json", "", compressibleBody},
		{"client accepts an encoding the original never offered", "zstd", "application/json", "", compressibleBody},
	}

	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		if c.accept != "" {
			req.Header.Set("Accept-Encoding", c.accept)
		}

		rec := httptest.NewRecorder()
		compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", c.contentType)
			if c.encoding != "" {
				w.Header().Set("Content-Encoding", c.encoding)
			}
			_, _ = w.Write([]byte(c.body))
		})).ServeHTTP(rec, req)

		assert.Equal(t, 200, rec.Code, c.name)
		assert.Equal(t, c.encoding, rec.Header().Get("Content-Encoding"), c.name)
		assert.Equal(t, c.body, rec.Body.String(), c.name)
		assert.Equal(t, strconv.Itoa(len(c.body)), rec.Header().Get("Content-Length"), c.name)
	}
}

func TestCompressOmitsLengthWhereABodyIsNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, httptest.NewRequest("DELETE", "/", nil))

	assert.Equal(t, 204, rec.Code)
	assert.Equal(t, "", rec.Header().Get("Content-Length"))
	assert.Equal(t, "", rec.Body.String())
}

func TestAcceptsEncoding(t *testing.T) {
	cases := []struct {
		header   string
		name     string
		expected bool
	}{
		{"", "gzip", false},
		{"gzip", "gzip", true},
		{"br, gzip", "gzip", true},
		{"deflate, gzip, br", "br", true},
		{"gzip;q=1.0", "gzip", false},
		{"x-gzip", "gzip", false},
		{"gzipped", "gzip", false},
		{"identity", "gzip", false},
	}

	for _, c := range cases {
		assert.Equal(t, c.expected, acceptsEncoding(c.header, c.name), c.header+" / "+c.name)
	}
}

func TestCORSAdvertisesOriginOnEveryResponse(t *testing.T) {
	cases := []struct {
		name   string
		origin string
	}{
		{"without an origin", ""},
		{"with an origin", "https://example.test"},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/books", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}

		rec := httptest.NewRecorder()
		cors(okHandler()).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, c.name)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"), c.name)
		assert.Equal(t, "Origin", rec.Header().Get("Vary"), c.name)
		assert.Equal(t, "OK", rec.Body.String(), c.name)
		assert.Equal(t, "", rec.Header().Get("Access-Control-Allow-Methods"), c.name)
	}
}

func TestCORSAnswersPreflightWithoutReachingTheRouter(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})

	req := httptest.NewRequest(http.MethodOptions, "/books", nil)
	rec := httptest.NewRecorder()
	cors(next).ServeHTTP(rec, req)

	assert.False(t, reached)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET,POST,HEAD,PUT,DELETE,PATCH", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Origin, Access-Control-Request-Method, Access-Control-Request-Headers", rec.Header().Get("Vary"))
	assert.Equal(t, "", rec.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "", rec.Body.String())
}

func TestCORSEchoesRequestedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/books", nil)
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-Custom,Content-Type")

	rec := httptest.NewRecorder()
	cors(okHandler()).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "X-Custom,Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestAppendVary(t *testing.T) {
	cases := []struct {
		existing string
		fields   []string
		expected string
	}{
		{"", []string{"Origin"}, "Origin"},
		{"Origin", []string{"Origin"}, "Origin"},
		{"Origin", []string{"Accept-Encoding"}, "Origin, Accept-Encoding"},
		{"Origin, Accept-Encoding", []string{"Origin"}, "Origin, Accept-Encoding"},
		{"Accept-Encoding, Origin", []string{"Origin"}, "Accept-Encoding, Origin"},
		{"Accept-Encoding, Origin, Range", []string{"Origin"}, "Accept-Encoding, Origin, Range"},
		{"", []string{"Origin", "Origin"}, "Origin"},
	}

	for _, c := range cases {
		header := http.Header{}
		if c.existing != "" {
			header.Set("Vary", c.existing)
		}

		appendVary(header, c.fields...)

		assert.Equal(t, c.expected, header.Get("Vary"), c.existing)
	}
}
