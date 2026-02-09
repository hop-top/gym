package serve

import (
	"encoding/json"
	"net/http"

	"github.com/hop-top/gym/internal/registry"
)

// Options configures the serve handlers.
type Options struct {
	DataHome string
	Name     string
}

type manifest struct {
	Repository repository `json:"repository"`
	Skills     []skill    `json:"skills"`
}

type repository struct {
	Name  string `json:"name"`
	Stars int    `json:"stars"`
}

type skill struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	SourceURL   string `json:"source_url"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description,omitempty"`
}

func manifestFromIndex(name string, idx registry.Index) manifest {
	out := manifest{Repository: repository{Name: name, Stars: 0}}
	out.Skills = make([]skill, 0, len(idx.Skills))
	for _, s := range idx.Skills {
		if s.Name == "" || s.SourceURL == "" {
			continue
		}
		out.Skills = append(out.Skills, skill{
			Name:        s.Name,
			Version:     s.Version,
			SourceURL:   s.SourceURL,
			Path:        s.Path,
			Description: s.Description,
		})
	}
	return out
}

// Handler returns an http.Handler that serves the skills registry.
func Handler(opts Options) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("/skills.json", func(w http.ResponseWriter, _ *http.Request) {
		idx, err := registry.LoadIndex(opts.DataHome)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		name := opts.Name
		if name == "" {
			name = "gym-registry"
		}
		doc := manifestFromIndex(name, idx)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc)
	})

	mux.HandleFunc("/_index.json", func(w http.ResponseWriter, _ *http.Request) {
		idx, err := registry.LoadIndex(opts.DataHome)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(idx)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("gym serve\n\nendpoints:\n  /skills.json\n  /_index.json\n  /health\n"))
	})

	return mux
}
