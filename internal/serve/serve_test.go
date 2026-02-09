package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hop.top/gym/internal/registry"
	"hop.top/gym/internal/serve"
)

func writeIndex(t *testing.T, dataHome string, skills []registry.SkillEntry) {
	t.Helper()
	idx := registry.Index{UpdatedAt: time.Now().UTC(), Skills: skills}
	if err := registry.SaveIndex(dataHome, idx); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsJSONServesSpecShape(t *testing.T) {
	dataHome := t.TempDir()
	writeIndex(t, dataHome, []registry.SkillEntry{
		{Name: "alpha", Version: "1.0.0", SourceURL: "https://example.com/alpha.git"},
		{Name: "beta", Version: "0.1.0", SourceURL: "https://example.com/beta.git", Description: "desc"},
	})

	srv := httptest.NewServer(serve.Handler(serve.Options{DataHome: dataHome, Name: "test-reg"}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/skills.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct == "" {
		t.Fatalf("missing Content-Type")
	}

	var doc struct {
		Repository struct {
			Name  string `json:"name"`
			Stars int    `json:"stars"`
		} `json:"repository"`
		Skills []struct {
			Name      string `json:"name"`
			Version   string `json:"version"`
			SourceURL string `json:"source_url"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if doc.Repository.Name != "test-reg" {
		t.Fatalf("repository.name = %q, want %q", doc.Repository.Name, "test-reg")
	}
	if len(doc.Skills) != 2 {
		t.Fatalf("skills len = %d, want 2", len(doc.Skills))
	}
}

func TestSkillsJSONSkipsInvalidEntries(t *testing.T) {
	dataHome := t.TempDir()
	writeIndex(t, dataHome, []registry.SkillEntry{{Name: "", Version: "1.0.0", SourceURL: ""}})

	srv := httptest.NewServer(serve.Handler(serve.Options{DataHome: dataHome, Name: ""}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/skills.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var doc struct {
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
		Skills []any `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if doc.Repository.Name != "gym-registry" {
		t.Fatalf("default name = %q, want %q", doc.Repository.Name, "gym-registry")
	}
	if len(doc.Skills) != 0 {
		t.Fatalf("skills len = %d, want 0", len(doc.Skills))
	}
}

func TestIndexEndpointReturnsIndexJSON(t *testing.T) {
	dataHome := t.TempDir()
	writeIndex(t, dataHome, []registry.SkillEntry{
		{Name: "alpha", Version: "1.0.0", SourceURL: "https://example.com/alpha.git"},
	})

	srv := httptest.NewServer(serve.Handler(serve.Options{DataHome: dataHome, Name: ""}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/_index.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var idx registry.Index
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "alpha" {
		t.Fatalf("idx.Skills = %#v", idx.Skills)
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(serve.Handler(serve.Options{DataHome: t.TempDir()}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandlerDoesNotCreateFiles(t *testing.T) {
	dataHome := t.TempDir()
	srv := httptest.NewServer(serve.Handler(serve.Options{DataHome: dataHome}))
	t.Cleanup(srv.Close)
	_, _ = http.Get(srv.URL + "/skills.json")

	ents, err := os.ReadDir(dataHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if filepath.Base(e.Name()) == "registry-index.json" {
			t.Fatalf("handler should not write index file")
		}
	}
}
