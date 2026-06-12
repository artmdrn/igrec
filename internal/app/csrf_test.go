package app

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"igrec.net/igrec/internal/store"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "igrec.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tmpl, err := template.ParseGlob("../../web/templates/*.html")
	if err != nil {
		t.Fatal(err)
	}

	return &App{
		cfg:            Config{BaseURL: "http://localhost:8080"},
		db:             db,
		templates:      tmpl,
		operatorEmails: map[string]struct{}{},
	}
}

func TestLoginPostRejectsMissingCSRF(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=x%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	a.login(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestLoginPostAcceptsValidCSRF(t *testing.T) {
	a := testApp(t)
	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/login", nil)
	a.login(wGet, reqGet)

	resp := wGet.Result()
	var csrf *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == csrfCookie {
			csrf = c
			break
		}
	}
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /login")
	}

	form := url.Values{}
	form.Set("email", "missing@example.com")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.login(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
}

func TestOperatorInvitesRequiresLogin(t *testing.T) {
	a := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/operator/invites", nil)
	w := httptest.NewRecorder()

	a.operatorInvites(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, w.Code)
	}
}

func TestOperatorInvitesRequiresOperatorEmail(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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

	req := httptest.NewRequest(http.MethodGet, "/operator/invites", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	w := httptest.NewRecorder()
	a.operatorInvites(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestOperatorInvitesCreatesInvite(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("operator", "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	a.operatorEmails[strings.ToLower(user.Email)] = struct{}{}
	sessionToken, sessionHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.CreateSession(sessionHash, user.ID, farFuture()); err != nil {
		t.Fatal(err)
	}

	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/operator/invites", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.operatorInvites(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /operator/invites")
	}

	form := url.Values{}
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/operator/invites", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()
	a.operatorInvites(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}

	body, err := io.ReadAll(wPost.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/join?invite=") {
		t.Fatalf("expected invite link in response body, got %q", string(body))
	}
}

func TestSettingsDeleteAccountRequiresMatchingUsername(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("action", "delete-account")
	form.Set("confirm_username", "wrong")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
	body, err := io.ReadAll(wPost.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "type your username to delete this account") {
		t.Fatalf("expected delete confirmation error, got %q", string(body))
	}
	if _, err := a.db.UserByID(user.ID); err != nil {
		t.Fatalf("expected user to remain after failed confirmation, got %v", err)
	}
}

func TestSettingsDeleteAccountRemovesUserAndSession(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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
	if _, err := a.db.CreatePost(user.ID, "ember", nil); err != nil {
		t.Fatal(err)
	}

	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("action", "delete-account")
	form.Set("confirm_username", user.Username)
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, wPost.Code)
	}
	if location := wPost.Result().Header.Get("Location"); location != "/" {
		t.Fatalf("expected redirect to /, got %q", location)
	}
	if _, err := a.db.UserByID(user.ID); err == nil {
		t.Fatal("expected user to be deleted")
	}
	if _, ok := a.currentUser(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("unexpected authenticated user without session cookie")
	}
	if _, err := a.db.UserBySessionHash(hashToken(sessionToken)); err == nil {
		t.Fatal("expected session to be deleted")
	}
}

func TestSettingsUpdateFediverseHandle(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("fediverse", "Alice@Mastodon.Example")
	form.Set("timestamp_preference", "date")
	form.Set("daily", "on")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, wPost.Code)
	}
	updated, err := a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FediverseAcct != "@Alice@mastodon.example" {
		t.Fatalf("expected normalized fediverse handle, got %q", updated.FediverseAcct)
	}
	if !updated.EmailOptIn {
		t.Fatal("expected daily email opt-in to persist")
	}
	if updated.TimestampPreference != "date" {
		t.Fatalf("expected timestamp preference to update, got %q", updated.TimestampPreference)
	}

	wSettings := httptest.NewRecorder()
	reqSettings := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqSettings.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wSettings, reqSettings)

	body, err := io.ReadAll(wSettings.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `value="@Alice@mastodon.example"`) {
		t.Fatalf("expected saved fediverse handle in settings form, got %q", string(body))
	}
}

func TestSettingsRejectsInvalidFediverseHandle(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpdateSettings(user.ID, "smart", false, "@existing@example.social", ""); err != nil {
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("fediverse", "not a handle")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
	body, err := io.ReadAll(wPost.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "fediverse handle must look like @name@example.social") {
		t.Fatalf("expected fediverse validation error, got %q", string(body))
	}
	unchanged, err := a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.FediverseAcct != "@existing@example.social" {
		t.Fatalf("expected existing handle to remain, got %q", unchanged.FediverseAcct)
	}
}

func TestSettingsPersistsRelMeLinks(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("rel_me", "https://GitHub.com/member\nhttps://social.example/@member#proof\nhttps://github.com/member")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, wPost.Code)
	}
	links, err := a.db.RelMeLinksByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 saved rel=me links, got %#v", links)
	}
	if links[0] != "https://github.com/member" {
		t.Fatalf("expected normalized github link, got %#v", links)
	}
	if links[1] != "https://social.example/@member" {
		t.Fatalf("expected fragment-stripped social link, got %#v", links)
	}

	wSettings := httptest.NewRecorder()
	reqSettings := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqSettings.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wSettings, reqSettings)

	body, err := io.ReadAll(wSettings.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "https://github.com/member\nhttps://social.example/@member") {
		t.Fatalf("expected saved rel=me links in settings form, got %q", string(body))
	}
}

func TestSettingsUpdateMigrationTargetHandle(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("migration_target", "Next@Mastodon.Example")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, wPost.Code)
	}
	updated, err := a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MigrationTarget != "@Next@mastodon.example" {
		t.Fatalf("expected normalized migration target, got %q", updated.MigrationTarget)
	}

	wSettings := httptest.NewRecorder()
	reqSettings := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqSettings.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wSettings, reqSettings)

	body, err := io.ReadAll(wSettings.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `value="@Next@mastodon.example"`) {
		t.Fatalf("expected saved migration target in settings form, got %q", string(body))
	}
}

func TestSettingsUpdateMigrationTargetURL(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("migration_target", "https://Mastodon.Example/users/next")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusSeeOther {
		t.Fatalf("expected status %d, got %d", http.StatusSeeOther, wPost.Code)
	}
	updated, err := a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MigrationTarget != "https://mastodon.example/users/next" {
		t.Fatalf("expected normalized migration target URL, got %q", updated.MigrationTarget)
	}
}

func TestSettingsRejectsInvalidMigrationTarget(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpdateSettings(user.ID, "smart", false, "", "@existing@example.social"); err != nil {
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("migration_target", "not a target")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
	body, err := io.ReadAll(wPost.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "migration target must be a fediverse handle or full https URL") {
		t.Fatalf("expected migration target validation error, got %q", string(body))
	}
	unchanged, err := a.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.MigrationTarget != "@existing@example.social" {
		t.Fatalf("expected existing migration target to remain, got %q", unchanged.MigrationTarget)
	}
}

func TestSettingsRejectsInvalidRelMeLink(t *testing.T) {
	a := testApp(t)
	user, err := a.db.CreateUser("member", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.ReplaceRelMeLinks(user.ID, []string{"https://existing.example/member"}); err != nil {
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
	reqGet := httptest.NewRequest(http.MethodGet, "/settings", nil)
	reqGet.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	a.settings(wGet, reqGet)

	csrf := cookieByName(wGet.Result(), csrfCookie)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("expected csrf cookie from GET /settings")
	}

	form := url.Values{}
	form.Set("rel_me", "http://insecure.example/member")
	form.Set(csrfField, csrf.Value)
	reqPost := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	reqPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPost.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	reqPost.AddCookie(csrf)
	wPost := httptest.NewRecorder()

	a.settings(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, wPost.Code)
	}
	body, err := io.ReadAll(wPost.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "rel=me links must be full https URLs") {
		t.Fatalf("expected rel=me validation error, got %q", string(body))
	}
	links, err := a.db.RelMeLinksByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0] != "https://existing.example/member" {
		t.Fatalf("expected existing rel=me link to remain, got %#v", links)
	}
}

func cookieByName(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func farFuture() (at time.Time) {
	return time.Now().Add(24 * time.Hour)
}
