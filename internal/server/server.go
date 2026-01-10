// Package server exposes a published index over HTTP. The surface is small on
// purpose: one search endpoint that mirrors the command-line options, and one
// health endpoint. Every response, including every error, is JSON except the
// health probe, so a client never has to guess at the shape of a body.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"sift/internal/core"
)

// AppSearcher is the application behaviour the handler needs: search the
// corpus named by its root and return the report.
type AppSearcher interface {
	Search(root string, opts core.SearchOptions) (core.SearchReport, error)
}

// Handler returns the HTTP API for app.
//
// Routes:
//
//	GET /search   parameters root, q, limit, filter (repeatable, "field:value")
//	              and facet (repeatable, comma separated); answers with the
//	              JSON core.SearchReport.
//	GET /healthz  answers with the plain text "ok".
//
// Any other path is a JSON 404 and any other method a JSON 405. A nil app is
// accepted and reports every search as unavailable rather than panicking.
func Handler(app AppSearcher) http.Handler {
	return &handler{app: app}
}

// handler routes requests and owns the response encoding.
type handler struct {
	app AppSearcher
}

// ServeHTTP implements http.Handler.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		h.healthz(w, r)
	case "/search":
		h.search(w, r)
	default:
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
	}
}

// healthz answers the liveness probe.
func (h *handler) healthz(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	const body = "ok"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

// search parses the query parameters into core.SearchOptions and answers with
// the report the application produced.
func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	if h.app == nil {
		writeError(w, http.StatusInternalServerError, "search is unavailable: no application configured")
		return
	}
	params := r.URL.Query()
	opts := core.SearchOptions{Query: params.Get("q"), Facets: parseFacets(params["facet"])}
	limit, err := parseLimit(params.Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.Limit = limit
	filters, err := parseFilters(params["filter"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.Filters = filters
	report, err := h.app.Search(params.Get("root"), opts)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// readOnly rejects anything but GET and HEAD and reports whether the request
// may proceed.
func readOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, http.StatusMethodNotAllowed, "method not allowed: "+r.Method)
	return false
}

// parseLimit reads the limit parameter. An empty value means no limit, as does
// any value that is not positive.
func parseLimit(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("limit must be an integer, got %q", text)
	}
	return n, nil
}

// parseFilters turns repeated "field:value" parameters into exact-match
// filters. A value keeps its spacing because filters compare exactly, and a
// repeated field keeps the last value given.
func parseFilters(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, raw := range values {
		field, value, ok := strings.Cut(raw, ":")
		field = strings.TrimSpace(field)
		if !ok || field == "" {
			return nil, fmt.Errorf("filter must look like field:value, got %q", raw)
		}
		out[field] = value
	}
	return out, nil
}

// parseFacets collects the requested facet fields, splitting on commas and
// dropping blanks and repeats while keeping the order they were asked for.
func parseFacets(values []string) []string {
	var out []string
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		for _, field := range strings.Split(raw, ",") {
			field = strings.TrimSpace(field)
			if field == "" || seen[field] {
				continue
			}
			seen[field] = true
			out = append(out, field)
		}
	}
	return out
}

// statusFor maps an application error onto an HTTP status.
func statusFor(err error) int {
	switch {
	case errors.Is(err, core.ErrQuery), errors.Is(err, core.ErrUsage), errors.Is(err, core.ErrConfig):
		return http.StatusBadRequest
	case errors.Is(err, core.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// errorBody is the JSON shape of every error response.
type errorBody struct {
	Error  string
	Status int
}

// writeError answers with a JSON error body carrying the status.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message, Status: status})
}

// writeJSON encodes v with the two-space indentation and trailing newline the
// on-disk files use, so responses diff cleanly.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		const fallback = "{\n  \"Error\": \"response could not be encoded\",\n  \"Status\": 500\n}\n"
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(fallback)))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, fallback)
		return
	}
	data = append(data, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
