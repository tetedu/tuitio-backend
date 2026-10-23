package api

import "net/http"

// openAPISpec returns the OpenAPI 3.1 description of every route served by
// Routes. It is built in Go (rather than committed as a static file) so the
// route-coverage test in openapi_test.go can compare it against the live mux
// and fail if the two drift.
func openAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Tuitio Backend API",
			"version":     "1.0.0",
			"description": "Indexer and REST API for Tuitio, a milestone-gated tuition escrow on Stellar (testnet).",
		},
		"servers": []map[string]any{
			{"url": "/", "description": "This service"},
		},
		"paths":      openAPIPaths(),
		"components": map[string]any{"schemas": openAPISchemas()},
	}
}

func openAPIPaths() map[string]any {
	return map[string]any{
		"/": map[string]any{
			"get": map[string]any{
				"summary":     "Service descriptor",
				"operationId": "getRoot",
				"responses":   map[string]any{"200": responseRef("ServiceDescriptor")},
			},
		},
		"/healthz": map[string]any{
			"get": map[string]any{
				"summary":     "Liveness/readiness probe",
				"operationId": "getHealthz",
				"responses": map[string]any{
					"200": responseRef("Status"),
					"503": errorResponse(),
				},
			},
		},
		"/api/institutions": map[string]any{
			"get": map[string]any{
				"summary":     "List all registered institutions",
				"operationId": "listInstitutions",
				"responses":   map[string]any{"200": responseRef("Institutions")},
			},
		},
		"/api/institutions/{address}": map[string]any{
			"get": map[string]any{
				"summary":     "Get a single institution by Stellar address",
				"operationId": "getInstitution",
				"parameters":  []map[string]any{pathParam("address", "Stellar account address of the institution")},
				"responses": map[string]any{
					"200": responseRef("Institution"),
					"404": errorResponse(),
				},
			},
		},
		"/api/grants": map[string]any{
			"get": map[string]any{
				"summary":     "List grants, optionally filtered",
				"operationId": "listGrants",
				"parameters": []map[string]any{
					queryParam("sponsor", "Filter by sponsor Stellar address"),
					queryParam("institution", "Filter by institution Stellar address"),
					queryParam("status", "Filter by grant status (e.g. active)"),
				},
				"responses": map[string]any{"200": responseRef("Grants")},
			},
		},
		"/api/grants/{id}": map[string]any{
			"get": map[string]any{
				"summary":     "Get a single grant by id",
				"operationId": "getGrant",
				"parameters":  []map[string]any{pathParam("id", "Numeric grant id")},
				"responses": map[string]any{
					"200": responseRef("Grant"),
					"400": errorResponse(),
					"404": errorResponse(),
				},
			},
		},
		"/api/grants/{id}/terms": map[string]any{
			"get": map[string]any{
				"summary":     "List the milestone terms of a grant",
				"operationId": "listTerms",
				"parameters":  []map[string]any{pathParam("id", "Numeric grant id")},
				"responses": map[string]any{
					"200": responseRef("Terms"),
					"400": errorResponse(),
					"404": errorResponse(),
				},
			},
		},
		"/api/activity": map[string]any{
			"get": map[string]any{
				"summary":     "Recent indexed activity",
				"operationId": "listActivity",
				"parameters":  []map[string]any{queryParam("limit", "Maximum number of events to return (default 50)")},
				"responses":   map[string]any{"200": responseRef("ActivityList")},
			},
		},
		"/api/stats": map[string]any{
			"get": map[string]any{
				"summary":     "Aggregate counts across the read model",
				"operationId": "getStats",
				"responses":   map[string]any{"200": responseRef("Stats")},
			},
		},
		"/api/indexer": map[string]any{
			"get": map[string]any{
				"summary":     "Indexer progress and skipped-history report",
				"operationId": "getIndexerStatus",
				"responses":   map[string]any{"200": responseRef("IndexerStatus")},
			},
		},
		"/openapi.json": map[string]any{
			"get": map[string]any{
				"summary":     "This OpenAPI 3.1 document",
				"operationId": "getOpenAPI",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OpenAPI 3.1 document",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object"},
							},
						},
					},
				},
			},
		},
	}
}

func openAPISchemas() map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	integer := func() map[string]any { return map[string]any{"type": "integer"} }
	return map[string]any{
		"Error": map[string]any{
			"type":       "object",
			"properties": map[string]any{"error": str()},
		},
		"Status": map[string]any{
			"type":       "object",
			"properties": map[string]any{"status": str()},
		},
		"ServiceDescriptor": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":     str(),
				"description": str(),
				"network":     str(),
				"endpoints":   map[string]any{"type": "array", "items": str()},
				"app":         str(),
				"repository":  str(),
			},
		},
		"Institution": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"address":       str(),
				"payout":        str(),
				"name":          str(),
				"country":       str(),
				"status":        str(),
				"registered_at": integer(),
			},
		},
		"Institutions": arrayOf("Institution"),
		"Grant": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"grant_id":      integer(),
				"sponsor":       str(),
				"beneficiary":   str(),
				"institution":   str(),
				"token":         str(),
				"term_amount":   integer(),
				"terms_total":   integer(),
				"next_term":     integer(),
				"status":        str(),
				"created_at":    integer(),
				"locked_amount": integer(),
			},
		},
		"Grants": arrayOf("Grant"),
		"Term": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"grant_id":      integer(),
				"term_index":    integer(),
				"status":        str(),
				"attested_at":   integer(),
				"release_after": integer(),
			},
		},
		"Terms": arrayOf("Term"),
		"Activity": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ledger":   integer(),
				"tx_hash":  str(),
				"contract": str(),
				"event":    str(),
				"payload":  map[string]any{},
				"seen_at":  map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"ActivityList": arrayOf("Activity"),
		"Stats": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"institutions_total": integer(),
				"institutions_live":  integer(),
				"grants_total":       integer(),
				"grants_active":      integer(),
				"terms_released":     integer(),
				"terms_disputed":     integer(),
				"terms_refunded":     integer(),
			},
		},
		"IndexerStatus": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cursor_ledger":          integer(),
				"chain_tip":              integer(),
				"lag_ledgers":            integer(),
				"updated_at":             nullable("string", "date-time"),
				"stale_seconds":          nullable("integer", ""),
				"last_gap_from":          nullable("integer", ""),
				"last_gap_to":            nullable("integer", ""),
				"last_gap_at":            nullable("string", "date-time"),
				"last_reconcile_at":      nullable("string", "date-time"),
				"last_reconcile_repairs": nullable("integer", ""),
			},
		},
	}
}

func responseRef(ref string) map[string]any {
	return map[string]any{
		"description": "Success",
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + ref},
			},
		},
	}
}

func errorResponse() map[string]any {
	return map[string]any{
		"description": "Error",
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Error"},
			},
		},
	}
}

func queryParam(name, desc string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": desc,
		"schema":      map[string]any{"type": "string"},
	}
}

func pathParam(name, desc string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": desc,
		"schema":      map[string]any{"type": "string"},
	}
}

func arrayOf(ref string) map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"$ref": "#/components/schemas/" + ref},
	}
}

// nullable returns a 3.1-style nullable type (a union with null).
func nullable(t, format string) map[string]any {
	m := map[string]any{"type": []any{t, "null"}}
	if format != "" {
		m["format"] = format
	}
	return m
}

// openapi serves the OpenAPI 3.1 document at GET /openapi.json.
func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, openAPISpec())
}
