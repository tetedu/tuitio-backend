package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAPIServed checks that GET /openapi.json returns a structurally valid
// OpenAPI 3.1 document. This doubles as the CI spec-validation gate required by
// issue #16.
func TestOpenAPIServed(t *testing.T) {
	srv := New(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v, want 3.1.0", doc["openapi"])
	}
	for _, key := range []string{"info", "paths", "components"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("spec missing top-level %q", key)
		}
	}

	// info must carry the document metadata consumers rely on.
	info, ok := doc["info"].(map[string]any)
	if !ok {
		t.Fatal("info is not an object")
	}
	if info["title"] == nil || info["version"] == nil {
		t.Error("info.title and info.version are required")
	}

	// components.schemas must define every reference used in paths.
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	if len(schemas) == 0 {
		t.Fatal("components.schemas is empty")
	}
}

// TestOpenAPIRoutesMatchMux guarantees the spec and the live router cannot
// drift: every pattern registered in Routes() must appear in the document.
func TestOpenAPIRoutesMatchMux(t *testing.T) {
	srv := New(nil)

	doc := openAPISpec()
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		t.Fatal("spec has no paths")
	}

	var missing int
	for _, r := range srv.routes() {
		path := r.pattern
		if path == "/{$}" {
			path = "/"
		}
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("route %s %s not present in OpenAPI spec paths", r.method, r.pattern)
			missing++
			continue
		}
		if _, ok := pathItem[strings.ToLower(r.method)]; !ok {
			t.Errorf("method %s for %s not documented in spec", r.method, path)
			missing++
		}
	}
	if missing > 0 {
		t.Fatalf("%d registered route(s) missing from OpenAPI spec", missing)
	}
}
