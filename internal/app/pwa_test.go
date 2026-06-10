package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManifestIncludesPWAInstallMetadata(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	w := httptest.NewRecorder()

	a.manifest(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/manifest+json; charset=utf-8" {
		t.Fatalf("expected manifest content type, got %q", contentType)
	}
	for _, needle := range []string{
		`"id":"/write"`,
		`"start_url":"/write?source=pwa"`,
		`"scope":"/"`,
		`"display_override":["standalone","browser"]`,
		`"shortcuts":[{"name":"write","short_name":"write","url":"/write"}`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected manifest to contain %s, got %s", needle, body)
		}
	}
}

func TestServiceWorkerServesRootScopedWorker(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/service-worker.js", nil)
	w := httptest.NewRecorder()

	a.serviceWorker(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/javascript; charset=utf-8" {
		t.Fatalf("expected javascript content type, got %q", contentType)
	}
	if allowed := w.Header().Get("Service-Worker-Allowed"); allowed != "/" {
		t.Fatalf("expected root scope header, got %q", allowed)
	}
	for _, needle := range []string{
		`const CACHE = "igrec-shell-` + assetsVersion + `";`,
		`"/write",`,
		assetPath("igrec.css"),
		`self.addEventListener("fetch", (event) => {`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected worker to contain %s, got %s", needle, body)
		}
	}
}

func TestLayoutRegistersPWAScript(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	a.firehose(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), assetPath("pwa.js")) {
		t.Fatalf("expected layout to include pwa registration script, got %s", w.Body.String())
	}
}
