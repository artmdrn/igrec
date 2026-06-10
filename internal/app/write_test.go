package app

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"igrec.net/igrec/internal/store"
)

func TestWritePostShowsCommittedWordBeforeRedirect(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("cc00ffee", "cc@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, user.ID, farFuture()); err != nil {
		t.Fatal(err)
	}

	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/write", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.write(wGet, reqGet)
	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie")
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField(csrfField, csrf.Value); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("word", "Integration"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	reqPost := httptest.NewRequest(http.MethodPost, "/write", &body)
	reqPost.Header.Set("Content-Type", form.FormDataContentType())
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()
	a.write(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
	html := wPost.Body.String()
	if !strings.Contains(html, "Integration") {
		t.Fatalf("expected committed word screen, got %s", html)
	}
	if !strings.Contains(html, `http-equiv="refresh" content="6; url=/@cc00ffee/1-Integration"`) {
		t.Fatalf("expected refresh to post permalink, got %s", html)
	}
}

func TestImageFocusCSSUsesStoredFocus(t *testing.T) {
	got := string(imageFocusCSS(storePostWithFocus(0.25, 0.75)))
	if got != "object-position: 25.0% 75.0%;" {
		t.Fatalf("unexpected focus CSS %q", got)
	}
}

func storePostWithFocus(x, y float64) store.Post {
	return store.Post{
		ImageURL: sql.NullString{String: "/uploads/photo.jpg", Valid: true},
		FocusX:   x,
		FocusY:   y,
	}
}
