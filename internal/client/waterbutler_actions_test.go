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

func TestWaterbutler_Rename(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wb := client.NewWaterbutler("tok")
	err := wb.Rename(context.Background(), srv.URL+"/v1/files/f1/move", "counts_v2.h5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["action"] != "rename" {
		t.Errorf("action = %v, want rename", gotBody["action"])
	}
	if gotBody["rename"] != "counts_v2.h5" {
		t.Errorf("rename = %v, want counts_v2.h5", gotBody["rename"])
	}
}

func TestWaterbutler_Move(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wb := client.NewWaterbutler("tok")
	err := wb.Move(context.Background(), srv.URL+"/v1/files/f1/move", "", "/processed", "output.h5", "replace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["action"] != "move" {
		t.Errorf("action = %v, want move", gotBody["action"])
	}
	if gotBody["path"] != "/processed" {
		t.Errorf("path = %v, want /processed", gotBody["path"])
	}
	if gotBody["rename"] != "output.h5" {
		t.Errorf("rename = %v, want output.h5", gotBody["rename"])
	}
	if gotBody["conflict"] != "replace" {
		t.Errorf("conflict = %v, want replace", gotBody["conflict"])
	}
}

func TestWaterbutler_Move_CrossProject(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wb := client.NewWaterbutler("tok")
	err := wb.Move(context.Background(), srv.URL+"/v1/files/f1/move", "xyz34", "/data", "", "warn")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["resource"] != "xyz34" {
		t.Errorf("resource = %v, want xyz34", gotBody["resource"])
	}
	if _, hasRename := gotBody["rename"]; hasRename {
		t.Error("rename should be omitted when newName is empty")
	}
}

func TestWaterbutler_Copy(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	wb := client.NewWaterbutler("tok")
	err := wb.Copy(context.Background(), srv.URL+"/v1/files/f1/move", "", "/backup", "", "keep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["action"] != "copy" {
		t.Errorf("action = %v, want copy", gotBody["action"])
	}
}

func TestWaterbutler_CreateFolder(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	t.Setenv("DATAPIN_FILES_BASE", srv.URL)
	wb := client.NewWaterbutler("tok")
	// Subfolder create URL, derived from a parent folder's ID-based upload link.
	base := srv.URL + "/v1/resources/abc12/providers/osfstorage/d5/"
	err := wb.CreateFolder(context.Background(), client.AppendFolderName(base, "results"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if !strings.Contains(gotPath, "kind=folder") || !strings.Contains(gotPath, "name=results") {
		t.Errorf("expected kind=folder&name=results, got %q", gotPath)
	}
	// Addresses the parent by its opaque ID, not a name.
	if !strings.Contains(gotPath, "/osfstorage/d5/") {
		t.Errorf("expected ID-based folder path /osfstorage/d5/, got %q", gotPath)
	}
}

func TestWaterbutler_CreateFolder_Root(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	t.Setenv("DATAPIN_FILES_BASE", srv.URL)
	wb := client.NewWaterbutler("tok")
	err := wb.CreateFolder(context.Background(), client.AppendFolderName(client.RootUploadURL("abc12"), "toplevel"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Root folder: path should end with osfstorage/ (no subdir segment)
	if !strings.HasSuffix(gotPath, "/osfstorage/") {
		t.Errorf("expected path to end with /osfstorage/, got %q", gotPath)
	}
}

func TestWaterbutler_Error4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"errors":[{"detail":"name already exists"}]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	wb := client.NewWaterbutler("tok")
	err := wb.Rename(context.Background(), srv.URL+"/v1/files/f1/move", "dup.csv")
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !strings.Contains(err.Error(), "name already exists") {
		t.Errorf("error = %q, want message about name already exists", err.Error())
	}
}
