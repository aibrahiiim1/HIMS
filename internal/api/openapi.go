package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// API Documentation (#30). The OpenAPI 3.0 document is generated from the live
// chi router via chi.Walk, so it always reflects the actual registered routes
// — no hand-maintained spec to drift. Each route becomes a path+operation,
// tagged by its resource, with {param} path parameters declared.

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// tagFor derives a grouping tag from a route pattern (the first meaningful
// segment after the /api/v1 prefix).
func tagFor(pattern string) string {
	p := strings.TrimPrefix(pattern, "/api/v1/")
	p = strings.TrimPrefix(p, "/")
	seg := p
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	if seg == "" || strings.HasPrefix(seg, "{") {
		return "general"
	}
	return seg
}

// summaryFor builds a human summary like "List devices" / "Create alert".
func summaryFor(method, pattern string) string {
	verb := map[string]string{
		http.MethodGet: "Get", http.MethodPost: "Create", http.MethodPut: "Update",
		http.MethodPatch: "Update", http.MethodDelete: "Delete",
	}[method]
	if verb == "" {
		verb = method
	}
	res := strings.TrimPrefix(pattern, "/api/v1/")
	if method == http.MethodGet && !strings.Contains(pattern, "{") {
		verb = "List"
	}
	return verb + " " + res
}

// openapiSpec handles GET /api/v1/openapi.json — the generated OpenAPI 3.0 doc.
func (s *Server) openapiSpec(w http.ResponseWriter, r *http.Request) {
	paths := map[string]map[string]any{}

	_ = chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi reports method ALL for some; skip non-HTTP verbs + the spec route.
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return nil
		}
		route = strings.TrimSuffix(route, "/")
		if route == "" {
			route = "/"
		}
		op := map[string]any{
			"summary":     summaryFor(method, route),
			"tags":        []string{tagFor(route)},
			"responses":   map[string]any{"200": map[string]any{"description": "OK"}},
			"operationId": strings.ToLower(method) + "_" + sanitizeOpID(route),
		}
		// Declare {param} path parameters.
		if params := pathParamRe.FindAllStringSubmatch(route, -1); len(params) > 0 {
			ps := make([]map[string]any, 0, len(params))
			for _, m := range params {
				ps = append(ps, map[string]any{
					"name": m[1], "in": "path", "required": true,
					"schema": map[string]any{"type": "string"},
				})
			}
			op["parameters"] = ps
		}
		if paths[route] == nil {
			paths[route] = map[string]any{}
		}
		paths[route][strings.ToLower(method)] = op
		return nil
	})

	// Collect the distinct tags for a stable, sorted tag list.
	tagSet := map[string]bool{}
	for route := range paths {
		tagSet[tagFor(route)] = true
	}
	tags := make([]map[string]any, 0, len(tagSet))
	keys := make([]string, 0, len(tagSet))
	for t := range tagSet {
		keys = append(keys, t)
	}
	sort.Strings(keys)
	for _, t := range keys {
		tags = append(tags, map[string]any{"name": t})
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "HIMS API",
			"version": "1.0",
			"description": "Hotel Infrastructure Management System REST API.\n\n" +
				"Base URL: `/api/v1`. Auth: internal (no token today); the optional `X-Actor` " +
				"request header attributes audit entries to a named operator. Credential and " +
				"channel secrets are AES-256-GCM encrypted at rest and never returned. " +
				"This document is generated from the live router, so it always matches the " +
				"deployed routes.",
		},
		"servers": []map[string]any{{"url": "/"}},
		"tags":    tags,
		"paths":   paths,
	}
	writeJSON(w, http.StatusOK, doc)
}

func sanitizeOpID(route string) string {
	r := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_")
	return strings.Trim(r.Replace(route), "_")
}
