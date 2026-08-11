package meta_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/meta"
)

func TestFUJI_Evaluate(t *testing.T) {
	var gotBody map[string]any
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"summary": {"score_percent": {"FAIR": 62.5, "F": 75.0, "A": 50.0, "I": 66.7, "R": 58.3}}}`))
	}))
	defer srv.Close()

	c := meta.NewFUJIClient(srv.URL, "user", "pass")
	score, err := c.Evaluate(context.Background(), "10.5281/zenodo.123456")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if score.FAIR != 62.5 || score.F != 75.0 || score.A != 50.0 {
		t.Errorf("score = %+v", score)
	}
	if gotUser != "user" || gotPass != "pass" {
		t.Errorf("basic auth = %q/%q", gotUser, gotPass)
	}
	if gotBody["object_identifier"] != "10.5281/zenodo.123456" {
		t.Errorf("request body = %v", gotBody)
	}
}

func TestFUJI_ServerErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	c := meta.NewFUJIClient(srv.URL, "", "")
	if _, err := c.Evaluate(context.Background(), "10.1/x"); err == nil {
		t.Fatal("500 must surface as an error")
	}
}
