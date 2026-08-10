package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
)

func strPtr(s string) *string { return &s }

func TestUpdateNode_Title(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "vnd.api+json") {
			t.Errorf("Content-Type = %q, want vnd.api+json", ct)
		}
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		writeNode(w, "abc12", "Updated Title", "")
	}))
	defer srv.Close()

	t.Setenv("GOSF_API_BASE", srv.URL+"/v2")
	c := client.New("tok")
	node, err := c.UpdateNode(context.Background(), "abc12", client.UpdateNodeAttrs{
		Title: strPtr("Updated Title"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.Attributes.Title != "Updated Title" {
		t.Errorf("title = %q, want Updated Title", node.Attributes.Title)
	}

	// Verify only title was sent in body
	data, _ := gotBody["data"].(map[string]any)
	attrs, _ := data["attributes"].(map[string]any)
	if _, hasDesc := attrs["description"]; hasDesc {
		t.Error("description should not be in body when not set")
	}
}

func TestUpdateNode_MultipleFields(t *testing.T) {
	var gotAttrs map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		data, _ := body["data"].(map[string]any)
		gotAttrs, _ = data["attributes"].(map[string]any)
		writeNode(w, "abc12", "T", "")
	}))
	defer srv.Close()

	t.Setenv("GOSF_API_BASE", srv.URL+"/v2")
	c := client.New("tok")
	_, err := c.UpdateNode(context.Background(), "abc12", client.UpdateNodeAttrs{
		Title:       strPtr("T"),
		Description: strPtr("D"),
		Category:    strPtr("analysis"),
		Tags:        []string{"tag1", "tag2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAttrs["title"] != "T" {
		t.Errorf("title = %v", gotAttrs["title"])
	}
	if gotAttrs["description"] != "D" {
		t.Errorf("description = %v", gotAttrs["description"])
	}
	if gotAttrs["category"] != "analysis" {
		t.Errorf("category = %v", gotAttrs["category"])
	}
	tags, _ := gotAttrs["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("tags len = %d, want 2", len(tags))
	}
}

func TestUpdateNode_NoFields(t *testing.T) {
	c := client.New("tok")
	_, err := c.UpdateNode(context.Background(), "abc12", client.UpdateNodeAttrs{})
	if err == nil {
		t.Fatal("expected error when no fields provided")
	}
}

func TestUpdateNode_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("GOSF_API_BASE", srv.URL+"/v2")
	c := client.New("tok")
	_, err := c.UpdateNode(context.Background(), "notfound", client.UpdateNodeAttrs{Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error on 404")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok || apiErr.StatusCode != 404 {
		t.Errorf("expected APIError 404, got %v", err)
	}
}

// writeNode writes a minimal node JSON:API response.
func writeNode(w http.ResponseWriter, id, title, desc string) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"data": map[string]any{
			"id": id,
			"attributes": map[string]any{
				"title":         title,
				"description":   desc,
				"date_created":  "2024-01-01T00:00:00",
				"date_modified": "2024-01-01T00:00:00",
				"public":        true,
				"category":      "project",
			},
		},
	})
}
