package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"sift/internal/core"
)

// fakeApp records the options it was called with and replays a canned answer.
type fakeApp struct {
	root   string
	opts   core.SearchOptions
	calls  int
	report core.SearchReport
	err    error
}

// Search implements AppSearcher.
func (f *fakeApp) Search(root string, opts core.SearchOptions) (core.SearchReport, error) {
	f.calls++
	f.root = root
	f.opts = opts
	return f.report, f.err
}

// get sends a GET request to the handler and returns the recorded response.
func get(app AppSearcher, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Handler(app).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// decodeError decodes a JSON error body.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal(%q) = %v, want nil", rec.Body.String(), err)
	}
	return body
}

func TestHealthzAnswersOK(t *testing.T) {
	rec := get(&fakeApp{}, "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "text/plain; charset=utf-8")
	}
}

func TestUnknownPathIsJSONNotFound(t *testing.T) {
	rec := get(&fakeApp{}, "/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json; charset=utf-8")
	}
	body := decodeError(t, rec)
	if body.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", body.Status, http.StatusNotFound)
	}
	if !strings.Contains(body.Error, "/nope") {
		t.Errorf("Error = %q, want it to name the requested path", body.Error)
	}
}

func TestNonReadMethodIsRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(&fakeApp{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/search", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if body := decodeError(t, rec); body.Status != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", body.Status, http.StatusMethodNotAllowed)
	}
}

func TestSearchParsesEveryParameter(t *testing.T) {
	app := &fakeApp{}
	rec := get(app, "/search?root=/corpora/docs&q=alpha+beta&limit=5&filter=kind:markdown&filter=language:go&facet=kind,language&facet=kind")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if app.calls != 1 {
		t.Fatalf("calls = %d, want 1", app.calls)
	}
	if app.root != "/corpora/docs" {
		t.Errorf("root = %q, want %q", app.root, "/corpora/docs")
	}
	if app.opts.Query != "alpha beta" {
		t.Errorf("Query = %q, want %q", app.opts.Query, "alpha beta")
	}
	if app.opts.Limit != 5 {
		t.Errorf("Limit = %d, want 5", app.opts.Limit)
	}
	wantFilters := map[string]string{"kind": "markdown", "language": "go"}
	if !reflect.DeepEqual(app.opts.Filters, wantFilters) {
		t.Errorf("Filters = %v, want %v", app.opts.Filters, wantFilters)
	}
	wantFacets := []string{"kind", "language"}
	if !reflect.DeepEqual(app.opts.Facets, wantFacets) {
		t.Errorf("Facets = %v, want %v", app.opts.Facets, wantFacets)
	}
}

func TestSearchWithoutParametersUsesZeroOptions(t *testing.T) {
	app := &fakeApp{}
	if rec := get(app, "/search"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if app.opts.Query != "" || app.opts.Limit != 0 {
		t.Errorf("options = %+v, want an empty query and no limit", app.opts)
	}
	if app.opts.Filters != nil {
		t.Errorf("Filters = %v, want nil", app.opts.Filters)
	}
	if app.opts.Facets != nil {
		t.Errorf("Facets = %v, want nil", app.opts.Facets)
	}
	if app.root != "" {
		t.Errorf("root = %q, want the empty root", app.root)
	}
}

func TestSearchEncodesTheReport(t *testing.T) {
	app := &fakeApp{report: core.SearchReport{
		Options: core.SearchOptions{Query: "alpha", Limit: 1},
		Total:   2,
		Results: []core.SearchResult{{
			DocID:  "docs/a.md",
			Path:   "docs/a.md",
			Title:  "Alpha",
			Score:  1.5,
			Freq:   3,
			Fields: map[string]string{"kind": "markdown"},
		}},
		Facets: map[string]core.Facet{"kind": {Field: "kind", Counts: map[string]int{"markdown": 2}}},
	}}
	rec := get(app, "/search?q=alpha&limit=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	raw := rec.Body.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Errorf("body = %q, want a trailing newline", raw)
	}
	if !strings.Contains(raw, "\n  \"Total\": 2") {
		t.Errorf("body = %q, want two-space indented JSON", raw)
	}
	var got core.SearchReport
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal() = %v, want nil", err)
	}
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2", got.Total)
	}
	if len(got.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(got.Results))
	}
	if got.Results[0].DocID != "docs/a.md" || got.Results[0].Score != 1.5 || got.Results[0].Freq != 3 {
		t.Errorf("Results[0] = %+v, want docs/a.md with score 1.5 and freq 3", got.Results[0])
	}
	if got.Facets["kind"].Counts["markdown"] != 2 {
		t.Errorf("Facets = %v, want kind:markdown counted twice", got.Facets)
	}
}

func TestSearchRejectsMalformedParameters(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantSub string
	}{
		{name: "limit is not a number", target: "/search?limit=lots", wantSub: "limit must be an integer"},
		{name: "filter has no colon", target: "/search?filter=kind", wantSub: "field:value"},
		{name: "filter has an empty field", target: "/search?filter=:markdown", wantSub: "field:value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &fakeApp{}
			rec := get(app, tt.target)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if app.calls != 0 {
				t.Errorf("calls = %d, want 0 because the request never reached the application", app.calls)
			}
			body := decodeError(t, rec)
			if body.Status != http.StatusBadRequest {
				t.Errorf("Status = %d, want %d", body.Status, http.StatusBadRequest)
			}
			if !strings.Contains(body.Error, tt.wantSub) {
				t.Errorf("Error = %q, want it to contain %q", body.Error, tt.wantSub)
			}
		})
	}
}

func TestSearchMapsApplicationErrorsToStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "query", err: &core.QueryError{Position: 4, Reason: "unbalanced quote"}, want: http.StatusBadRequest},
		{name: "usage", err: core.ErrUsage, want: http.StatusBadRequest},
		{name: "config", err: &core.ConfigError{Field: "root", Reason: "missing"}, want: http.StatusBadRequest},
		{name: "not found", err: core.ErrNotFound, want: http.StatusNotFound},
		{name: "integrity", err: &core.IntegrityError{Path: "seg-0001.json", Reason: "digest mismatch"}, want: http.StatusInternalServerError},
		{name: "unexpected", err: errors.New("disk on fire"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(&fakeApp{err: tt.err}, "/search?q=alpha")
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			body := decodeError(t, rec)
			if body.Status != tt.want {
				t.Errorf("Status = %d, want %d", body.Status, tt.want)
			}
			if body.Error != tt.err.Error() {
				t.Errorf("Error = %q, want %q", body.Error, tt.err.Error())
			}
		})
	}
}

func TestSearchWithoutApplicationFailsCleanly(t *testing.T) {
	rec := get(nil, "/search?q=alpha")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if body := decodeError(t, rec); !strings.Contains(body.Error, "unavailable") {
		t.Errorf("Error = %q, want it to report the missing application", body.Error)
	}
	if rec := get(nil, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d because the probe does not need an application", rec.Code, http.StatusOK)
	}
}
