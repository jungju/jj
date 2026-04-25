package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndexShowsDocsAndRuns(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "20260425-120000-bbbbbb")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "README.md") || !strings.Contains(body, "plan.md") || !strings.Contains(body, "playground/plan.md") {
		t.Fatalf("index missing docs:\n%s", body)
	}
	first := strings.Index(body, "20260425-120000-bbbbbb")
	second := strings.Index(body, "20260425-110000-aaaaaa")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("selected/latest run should appear first:\n%s", body)
	}
}

func TestDocShowsRedactedMarkdown(t *testing.T) {
	dir := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("token sk-proj-abcdef1234567890\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=README.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, "sk-proj") || !strings.Contains(body, "[redacted-openai-key]") {
		t.Fatalf("doc was not redacted:\n%s", body)
	}
}

func TestDocRendersMarkdownAndEscapesHTML(t *testing.T) {
	dir := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Title\n\n<script>alert(1)</script>\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=README.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "<h1>Title</h1>") {
		t.Fatalf("markdown heading was not rendered:\n%s", body)
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("HTML was not escaped:\n%s", body)
	}
}

func TestArtifactShowsRunFile(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-120000-bbbbbb&path=docs/TASK.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "Do the task") {
		t.Fatalf("artifact body missing task:\n%s", body)
	}
}

func TestRunShowsDocsArtifactsFirst(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/run?id=20260425-120000-bbbbbb", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	spec := strings.Index(body, "docs/SPEC.md")
	task := strings.Index(body, "docs/TASK.md")
	eval := strings.Index(body, "docs/EVAL.md")
	if spec < 0 || task < 0 || eval < 0 || !(spec < task && task < eval) {
		t.Fatalf("docs artifacts missing or not first in expected order:\n%s", body)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	for _, target := range []string{
		"/doc?path=../README.md",
		"/artifact?run=20260425-120000-bbbbbb&path=../manifest.json",
		"/run?id=../bad",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		server.Handler().ServeHTTP(rec, req)
		if rec.Code < 400 {
			t.Fatalf("expected rejection for %s, got %d", target, rec.Code)
		}
	}
}

func TestSymlinkTraversalRejected(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	outsideDoc := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsideDoc, []byte("# Outside\n"), 0o644); err != nil {
		t.Fatalf("write outside doc: %v", err)
	}
	linkPath := filepath.Join(dir, "docs", "outside.md")
	if err := os.Symlink(outsideDoc, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=docs/outside.md", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Fatalf("expected symlink escape rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExecuteStartsAndStopsWithContext(t *testing.T) {
	dir := newTestWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out strings.Builder
	errCh := make(chan error, 1)
	go func() {
		errCh <- Execute(ctx, Config{CWD: dir, Addr: "127.0.0.1:0", Stdout: &out})
	}()

	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "jj: serving docs at http://") {
		select {
		case err := <-errCh:
			t.Fatalf("server exited early: %v", err)
		case <-deadline:
			t.Fatal("server did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("execute should stop cleanly on context cancel: %v", err)
	}
}

func newTestServer(t *testing.T, dir, runID string) *Server {
	t.Helper()
	server, err := New(dir, runID)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func newTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Root\n")
	writeFile(t, dir, "plan.md", "# Product Plan\n")
	writeFile(t, dir, "docs/guide.md", "# Guide\n")
	writeFile(t, dir, "playground/plan.md", "# Plan\n")
	writeFile(t, dir, ".git/ignored.md", "# ignored\n")
	writeFile(t, dir, ".jj/runs/20260425-110000-aaaaaa/manifest.json", `{"run_id":"20260425-110000-aaaaaa","status":"success","started_at":"2026-04-25T11:00:00Z"}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/manifest.json", `{"run_id":"20260425-120000-bbbbbb","status":"failed","started_at":"2026-04-25T12:00:00Z"}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/SPEC.md", "# SPEC\n\nDo the spec.\n")
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/TASK.md", "# TASK\n\nDo the task.\n")
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/EVAL.md", "# EVAL\n\nPASS.\n")
	return dir
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
