package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The migrate command needs three pieces of node metadata the client did not
// previously fetch: the node's tags, its contributor list (names for creator
// skeletons), and its child components.

func TestGetNode_DecodesTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nodes/abc12/" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id": "abc12",
				"attributes": map[string]any{
					"title": "Tagged Project",
					"tags":  []string{"rna-seq", "mouse"},
				},
			},
		})
	}))
	defer srv.Close()

	c := New("")
	c.baseURL = srv.URL
	node, err := c.GetNode(context.Background(), "abc12")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if len(node.Attributes.Tags) != 2 || node.Attributes.Tags[0] != "rna-seq" || node.Attributes.Tags[1] != "mouse" {
		t.Errorf("Tags = %v, want [rna-seq mouse]", node.Attributes.Tags)
	}
}

func TestGetContributors(t *testing.T) {
	pages := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nodes/abc12/contributors/" {
			http.NotFound(w, r)
			return
		}
		pages++
		// Two pages, to prove pagination is followed.
		if r.URL.Query().Get("page") == "2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id":         "abc12-u2",
					"attributes": map[string]any{"bibliographic": false},
					"embeds": map[string]any{
						"users": map[string]any{
							"data": map[string]any{
								"id":         "u2",
								"attributes": map[string]any{"full_name": "Grace Hopper"},
							},
						},
					},
				}},
				"links": map[string]any{"next": nil},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id":         "abc12-u1",
				"attributes": map[string]any{"bibliographic": true},
				"embeds": map[string]any{
					"users": map[string]any{
						"data": map[string]any{
							"id": "u1",
							"attributes": map[string]any{
								"full_name":   "Ada Lovelace",
								"given_name":  "Ada",
								"family_name": "Lovelace",
							},
						},
					},
				},
			}},
			"links": map[string]any{"next": srv.URL + "/nodes/abc12/contributors/?page=2"},
		})
	}))
	defer srv.Close()

	c := New("")
	c.baseURL = srv.URL
	contribs, err := c.GetContributors(context.Background(), "abc12")
	if err != nil {
		t.Fatalf("GetContributors: %v", err)
	}
	if len(contribs) != 2 {
		t.Fatalf("got %d contributors, want 2 (pages served: %d)", len(contribs), pages)
	}
	first := contribs[0]
	if !first.Attributes.Bibliographic {
		t.Error("first contributor should be bibliographic")
	}
	u := first.Embeds.Users.Data.Attributes
	if u.FullName != "Ada Lovelace" || u.GivenName != "Ada" || u.FamilyName != "Lovelace" {
		t.Errorf("embedded user = %+v", u)
	}
	if got := contribs[1].Embeds.Users.Data.Attributes.FullName; got != "Grace Hopper" {
		t.Errorf("second contributor full_name = %q", got)
	}
}

func TestGetContributors_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"detail": "no access"}},
		})
	}))
	defer srv.Close()

	c := New("")
	c.baseURL = srv.URL
	if _, err := c.GetContributors(context.Background(), "abc12"); err == nil {
		t.Fatal("want error on 403, got nil")
	}
}

func TestGetChildren(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nodes/abc12/children/" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id": "xyz34",
				"attributes": map[string]any{
					"title": "Component A",
					"tags":  []string{"sub"},
				},
			}},
			"links": map[string]any{"next": nil},
		})
	}))
	defer srv.Close()

	c := New("")
	c.baseURL = srv.URL
	kids, err := c.GetChildren(context.Background(), "abc12")
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(kids) != 1 || kids[0].ID != "xyz34" || kids[0].Attributes.Title != "Component A" {
		t.Errorf("children = %+v", kids)
	}
}
