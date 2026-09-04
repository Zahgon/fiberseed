package server

import (
	"bytes"
	"fiberseed/pkg"
	"fmt"
	"hash/crc32"
	"net/http"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"
	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zlib"
)

const weakPrefix = "W/"

// etagTable is the polynomial the original validator was built from. A stock
// table produces a different checksum for the same body, so every cached entity
// would be invalidated by the switch.
var etagTable = crc32.MakeTable(0xD5828281)

// recoverer turns a panic raised by a handler into the application's JSON error
// response instead of a dropped connection.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				pkg.WriteError(w, pkg.Unexpected(fmt.Sprintf("%v", rec)))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// nonStrictRouting reproduces the lookup rules the original router applied
// before a request reached the route table: trailing slashes are ignored, and a
// path that still matches nothing is retried without regard to letter case. A
// path built only from slashes is left as it arrived, since the router reads an
// empty path as the root route and rewriting would answer requests that are
// expected to go unmatched. The rewrite is published as the routing path rather
// than by editing the request, so it is in place before any later middleware
// looks a route up.
func nonStrictRouting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			next.ServeHTTP(w, r)
			return
		}

		routePath := r.URL.RawPath
		if routePath == "" {
			routePath = r.URL.Path
		}

		if len(routePath) > 1 {
			if trimmed := strings.TrimRight(routePath, "/"); trimmed != "" {
				routePath = trimmed
			}
		}

		if lowered := toLowerASCII(routePath); lowered != routePath &&
			!routeExists(rctx, r.Method, routePath) && routeExists(rctx, r.Method, lowered) {
			routePath = lowered
		}

		rctx.RoutePath = routePath

		next.ServeHTTP(w, r)
	})
}

// routeExists reports whether path is served for method. A HEAD request also
// counts as served when only the GET route is registered, because the router
// answers one from the other.
func routeExists(rctx *chi.Context, method, path string) bool {
	if rctx.Routes == nil {
		return false
	}

	if rctx.Routes.Match(chi.NewRouteContext(), method, path) {
		return true
	}

	return method == http.MethodHead && rctx.Routes.Match(chi.NewRouteContext(), http.MethodGet, path)
}

// toLowerASCII folds letter case the way the original router did, one byte at a
// time, so the result keeps the length and the offsets of the input.
func toLowerASCII(s string) string {
	lowered := []byte(s)
	changed := false

	for i, c := range lowered {
		if c >= 'A' && c <= 'Z' {
			lowered[i] = c + ('a' - 'A')
			changed = true
		}
	}

	if !changed {
		return s
	}

	return string(lowered)
}

const (
	corsAllowOrigin  = "*"
	corsAllowMethods = "GET,POST,HEAD,PUT,DELETE,PATCH"
)

// cors answers cross-origin requests as the original did: every response
// advertises the permitted origin whether or not the request named one, and a
// preflight never reaches the router - it is completed here with the requested
// headers echoed back unfiltered.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions {
			appendVary(w.Header(), "Origin")
			w.Header().Set("Access-Control-Allow-Origin", corsAllowOrigin)

			next.ServeHTTP(w, r)

			return
		}

		appendVary(w.Header(), "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")
		w.Header().Set("Access-Control-Allow-Origin", corsAllowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)

		if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
			w.Header().Set("Access-Control-Allow-Headers", requested)
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// appendVary collects the named fields into a single Vary header, skipping any
// field the header already carries.
func appendVary(header http.Header, fields ...string) {
	value := header.Get("Vary")

	for _, field := range fields {
		switch {
		case value == "":
			value = field
		case value == field,
			strings.HasPrefix(value, field+","),
			strings.HasSuffix(value, " "+field),
			strings.Contains(value, " "+field+","):
		default:
			value += ", " + field
		}
	}

	header.Set("Vary", value)
}

// minCompressLen is the body size below which the original left a response
// alone: encoding a short payload usually makes it longer.
const minCompressLen = 200

// compress encodes a finished response when the client accepts an encoding the
// original offered. The body is produced in full first, so the choice is made
// on the complete payload exactly as before.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffered := &bufferedWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(buffered, r)

		body := buffered.body.Bytes()

		if encoding, encoded := encodeBody(w.Header(), r.Header.Get("Accept-Encoding"), body); encoded != nil {
			w.Header().Set("Content-Encoding", encoding)
			body = encoded
		}

		if len(body) > 0 && bodyAllowedForStatus(buffered.status) {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}

		w.WriteHeader(buffered.status)
		_, _ = w.Write(body)
	})
}

// encodeBody applies the first encoding the client accepts, in the order the
// original preferred them, and reports the name to advertise. It returns no
// body when the response must be left as it is.
func encodeBody(header http.Header, acceptEncoding string, body []byte) (string, []byte) {
	if header.Get("Content-Encoding") != "" || !compressibleContentType(header.Get("Content-Type")) {
		return "", nil
	}

	if len(body) < minCompressLen {
		return "", nil
	}

	switch {
	case acceptsEncoding(acceptEncoding, "br"):
		return "br", brotliBytes(body)
	case acceptsEncoding(acceptEncoding, "gzip"):
		return "gzip", gzipBytes(body)
	case acceptsEncoding(acceptEncoding, "deflate"):
		return "deflate", deflateBytes(body)
	}

	return "", nil
}

// compressibleContentType keeps the original's rule that only textual and
// application payloads are worth encoding. A response that names no type is
// treated as the default one, which is textual.
func compressibleContentType(contentType string) bool {
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}

	return strings.HasPrefix(contentType, "text/") || strings.HasPrefix(contentType, "application/")
}

// acceptsEncoding reads an Accept-Encoding header the way the original did: the
// name must appear as a whole entry, so a quality suffix or a missing separator
// disqualifies it.
func acceptsEncoding(acceptEncoding, name string) bool {
	n := strings.Index(acceptEncoding, name)
	if n < 0 {
		return false
	}

	if rest := acceptEncoding[n+len(name):]; len(rest) > 0 && rest[0] != ',' {
		return false
	}

	if n == 0 {
		return true
	}

	return acceptEncoding[n-1] == ' '
}

func brotliBytes(body []byte) []byte {
	var out bytes.Buffer

	zw := brotli.NewWriterLevel(&out, brotli.BestSpeed)
	_, _ = zw.Write(body)
	_ = zw.Close()

	return out.Bytes()
}

func gzipBytes(body []byte) []byte {
	var out bytes.Buffer

	zw, err := gzip.NewWriterLevel(&out, flate.BestSpeed)
	if err != nil {
		return nil
	}

	_, _ = zw.Write(body)
	_ = zw.Close()

	return out.Bytes()
}

func deflateBytes(body []byte) []byte {
	var out bytes.Buffer

	zw, err := zlib.NewWriterLevel(&out, flate.BestSpeed)
	if err != nil {
		return nil
	}

	_, _ = zw.Write(body)
	_ = zw.Close()

	return out.Bytes()
}

// bodyAllowedForStatus reports whether a response with this status may carry a
// body, and so whether its length should be announced.
func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == http.StatusNoContent, status == http.StatusNotModified:
		return false
	}

	return true
}

// etag adds a `"<length>-<crc32>"` response validator to successful responses
// that carry a body, and answers 304 when the client already holds that entity.
func etag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffered := &bufferedWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(buffered, r)

		body := buffered.body.Bytes()
		if buffered.status != http.StatusOK || len(body) == 0 {
			buffered.flush()
			return
		}

		tag := fmt.Sprintf("%q", strconv.Itoa(len(body))+"-"+strconv.FormatUint(uint64(crc32.Checksum(body, etagTable)), 10))

		if matchETag(r.Header.Get("If-None-Match"), tag) {
			// net/http strips Content-Type, Content-Length and Transfer-Encoding
			// from a 304 regardless of what is set here. The original kept
			// Content-Type; reproducing that would require hijacking the
			// connection, and the header is inert on a bodyless response.
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", tag)
		buffered.flush()
	})
}

// matchETag reports whether an If-None-Match header covers tag. A header sent in
// the weak form must name the validator exactly, with or without its own weak
// prefix; any other header matches when it contains the validator.
func matchETag(header, tag string) bool {
	if strings.HasPrefix(header, weakPrefix) {
		rest := header[len(weakPrefix):]

		return rest == tag || (len(tag) >= len(weakPrefix) && rest == tag[len(weakPrefix):])
	}

	return header != "" && strings.Contains(header, tag)
}

// bufferedWriter holds a response in memory so it can be hashed or encoded
// before it is sent, and replays it unchanged once that has been decided.
type bufferedWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (b *bufferedWriter) WriteHeader(status int) {
	if !b.wroteHeader {
		b.status = status
		b.wroteHeader = true
	}
}

func (b *bufferedWriter) Write(p []byte) (int, error) {
	b.wroteHeader = true
	return b.body.Write(p)
}

func (b *bufferedWriter) flush() {
	b.ResponseWriter.WriteHeader(b.status)
	_, _ = b.ResponseWriter.Write(b.body.Bytes())
}
