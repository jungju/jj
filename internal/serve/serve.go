package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/secrets"
)

const DefaultAddr = "127.0.0.1:7331"

type Config struct {
	CWD    string
	Addr   string
	RunID  string
	Stdout io.Writer
}

type Server struct {
	cwd   string
	runID string
	mux   *http.ServeMux
}

type docLink struct {
	Path string
}

type runLink struct {
	ID        string
	Status    string
	StartedAt string
}

type artifactLink struct {
	Path string
}

type pageData struct {
	Title       string
	CWD         string
	SelectedRun string
	Docs        []docLink
	Runs        []runLink
	Artifacts   []artifactLink
	Path        string
	RunID       string
	Content     string
	Error       string
}

func Execute(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.CWD) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg.CWD = cwd
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = DefaultAddr
	}
	server, err := New(cfg.CWD, cfg.RunID)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	fmt.Fprintf(cfg.Stdout, "jj: serving docs at http://%s\n", listener.Addr().String())

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func New(cwd, runID string) (*Server, error) {
	if strings.TrimSpace(cwd) == "" {
		return nil, errors.New("cwd is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cwd does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cwd is not a directory: %s", abs)
	}
	if strings.TrimSpace(runID) != "" {
		if err := artifact.ValidateRunID(runID); err != nil {
			return nil, err
		}
	}
	s := &Server{cwd: abs, runID: runID, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/doc", s.handleDoc)
	s.mux.HandleFunc("/run", s.handleRun)
	s.mux.HandleFunc("/artifact", s.handleArtifact)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	docs, err := s.discoverDocs()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.discoverRuns()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}
	s.render(w, pageData{
		Title:       "jj docs",
		CWD:         s.cwd,
		SelectedRun: s.runID,
		Docs:        docs,
		Runs:        runs,
	})
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	if strings.ToLower(filepath.Ext(rel)) != ".md" {
		s.renderError(w, http.StatusBadRequest, errors.New("only markdown documents are supported"))
		return
	}
	path, err := safeJoin(s.cwd, rel)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	s.render(w, pageData{
		Title:   rel,
		CWD:     s.cwd,
		Path:    filepath.ToSlash(rel),
		Content: secrets.Redact(string(data)),
	})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("id")
	if strings.TrimSpace(runID) == "" {
		runID = s.runID
	}
	if strings.TrimSpace(runID) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	artifacts, err := discoverArtifacts(runDir)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	s.render(w, pageData{
		Title:     "run " + runID,
		CWD:       s.cwd,
		RunID:     runID,
		Artifacts: artifacts,
	})
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run")
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(rel) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("run and path are required"))
		return
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	path, err := safeJoin(runDir, rel)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	s.render(w, pageData{
		Title:   runID + "/" + rel,
		CWD:     s.cwd,
		RunID:   runID,
		Path:    filepath.ToSlash(rel),
		Content: secrets.Redact(string(data)),
	})
}

func (s *Server) runDir(runID string) (string, error) {
	if err := artifact.ValidateRunID(runID); err != nil {
		return "", err
	}
	return safeJoin(filepath.Join(s.cwd, ".jj", "runs"), runID)
}

func (s *Server) discoverDocs() ([]docLink, error) {
	var docs []docLink
	err := filepath.WalkDir(s.cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".jj", "node_modules", "vendor":
				return filepath.SkipDir
			}
			if path != s.cwd && !isAllowedDocDir(s.cwd, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(name)) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(s.cwd, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isRootDoc(rel) || strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "playground/") {
			docs = append(docs, docLink{Path: rel})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

func (s *Server) discoverRuns() ([]runLink, error) {
	runsDir := filepath.Join(s.cwd, ".jj", "runs")
	entries, err := os.ReadDir(runsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs := make([]runLink, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := artifact.ValidateRunID(entry.Name()); err != nil {
			continue
		}
		run := runLink{ID: entry.Name()}
		manifestPath := filepath.Join(runsDir, entry.Name(), "manifest.json")
		if data, err := os.ReadFile(manifestPath); err == nil {
			var manifest struct {
				Status    string `json:"status"`
				StartedAt string `json:"started_at"`
			}
			_ = json.Unmarshal(data, &manifest)
			run.Status = manifest.Status
			run.StartedAt = manifest.StartedAt
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].ID == s.runID {
			return true
		}
		if runs[j].ID == s.runID {
			return false
		}
		return runs[i].ID > runs[j].ID
	})
	return runs, nil
}

func discoverArtifacts(runDir string) ([]artifactLink, error) {
	var artifacts []artifactLink
	err := filepath.WalkDir(runDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifactLink{Path: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifactRank(artifacts[i].Path) < artifactRank(artifacts[j].Path) ||
			(artifactRank(artifacts[i].Path) == artifactRank(artifacts[j].Path) && artifacts[i].Path < artifacts[j].Path)
	})
	return artifacts, nil
}

func artifactRank(path string) int {
	switch path {
	case "docs/SPEC.md":
		return 0
	case "docs/TASK.md":
		return 1
	case "docs/EVAL.md":
		return 2
	case "SPEC.md":
		return 3
	case "TASK.md":
		return 4
	case "EVAL.md":
		return 5
	case "manifest.json":
		return 6
	default:
		return 10
	}
}

func isAllowedDocDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "docs" || strings.HasPrefix(rel, "docs/") || rel == "playground" || strings.HasPrefix(rel, "playground/")
}

func isRootDoc(rel string) bool {
	switch rel {
	case "README.md", "plan.md", "SPEC.md", "TASK.md", "EVAL.md":
		return true
	default:
		return false
	}
}

func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	path := filepath.Join(root, clean)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relToRoot, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	return absPath, nil
}

func (s *Server) renderError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	s.render(w, pageData{Title: "error", CWD: s.cwd, Error: secrets.Redact(err.Error())})
}

func (s *Server) render(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"q": func(s string) string { return template.URLQueryEscaper(s) },
}).Parse(`<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: Canvas; color: CanvasText; }
    header { padding: 18px 24px; border-bottom: 1px solid color-mix(in srgb, CanvasText 18%, transparent); }
    main { max-width: 1120px; margin: 0 auto; padding: 24px; }
    h1 { margin: 0 0 6px; font-size: 22px; }
    h2 { margin-top: 28px; font-size: 16px; }
    .muted { color: color-mix(in srgb, CanvasText 62%, transparent); font-size: 13px; }
    .grid { display: grid; gap: 18px; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); }
    ul { list-style: none; padding: 0; margin: 10px 0 0; }
    li { padding: 7px 0; border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, transparent); }
    a { color: LinkText; text-decoration: none; }
    a:hover { text-decoration: underline; }
    pre { overflow: auto; padding: 18px; border: 1px solid color-mix(in srgb, CanvasText 16%, transparent); border-radius: 6px; line-height: 1.45; white-space: pre-wrap; word-break: break-word; }
    .error { color: #b42318; }
  </style>
</head>
<body>
  <header>
    <h1>{{.Title}}</h1>
    <div class="muted">{{.CWD}}</div>
  </header>
  <main>
    {{if .Error}}
      <p class="error">{{.Error}}</p>
    {{else if .Content}}
      <p><a href="/">← index</a>{{if .RunID}} · <a href="/run?id={{q .RunID}}">run {{.RunID}}</a>{{end}}</p>
      <div class="muted">{{.Path}}</div>
      <pre>{{.Content}}</pre>
    {{else if .Artifacts}}
      <p><a href="/">← index</a></p>
      <h2>Artifacts</h2>
      <ul>
      {{range .Artifacts}}
        <li><a href="/artifact?run={{q $.RunID}}&path={{q .Path}}">{{.Path}}</a></li>
      {{else}}
        <li class="muted">No artifacts found.</li>
      {{end}}
      </ul>
    {{else}}
      <div class="grid">
        <section>
          <h2>Docs</h2>
          <ul>
          {{range .Docs}}
            <li><a href="/doc?path={{q .Path}}">{{.Path}}</a></li>
          {{else}}
            <li class="muted">No markdown docs found.</li>
          {{end}}
          </ul>
        </section>
        <section>
          <h2>Runs</h2>
          <ul>
          {{range .Runs}}
            <li><a href="/run?id={{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}}</span></li>
          {{else}}
            <li class="muted">No jj runs found.</li>
          {{end}}
          </ul>
        </section>
      </div>
    {{end}}
  </main>
</body>
</html>`))
