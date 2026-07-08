package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const mastodonOAuthStateCookie = "igrec_mastodon_state"
const mastodonOAuthNextCookie = "igrec_mastodon_next"
const mastodonOAuthInstanceCookie = "igrec_mastodon_instance"
const mastodonOAuthClientIDCookie = "igrec_mastodon_client_id"
const mastodonOAuthClientSecretCookie = "igrec_mastodon_client_secret"

func (a *App) mastodonStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !a.allowAuthRate("mastodon-start:ip:"+clientKey(r), 20, 10*time.Minute) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	instance, err := normalizeMastodonInstance(r.FormValue("instance"))
	if err != nil {
		a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "MastodonInstance": r.FormValue("instance"), "Next": safeNext(r.FormValue("next"))}))
		return
	}
	redirectURI := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/mastodon/callback"
	clientID, clientSecret, err := registerMastodonOAuthApp(r.Context(), instance, redirectURI, strings.TrimRight(a.cfg.BaseURL, "/")+"/")
	if err != nil {
		a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "MastodonInstance": instance.Host, "Next": safeNext(r.FormValue("next"))}))
		return
	}
	state, _, err := newToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	expires := time.Now().Add(15 * time.Minute)
	secure := a.secureCookies()
	http.SetCookie(w, &http.Cookie{Name: mastodonOAuthStateCookie, Value: state, Path: "/auth/mastodon", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
	http.SetCookie(w, &http.Cookie{Name: mastodonOAuthNextCookie, Value: safeNext(r.FormValue("next")), Path: "/auth/mastodon", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
	http.SetCookie(w, &http.Cookie{Name: mastodonOAuthInstanceCookie, Value: instance.String(), Path: "/auth/mastodon", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
	http.SetCookie(w, &http.Cookie{Name: mastodonOAuthClientIDCookie, Value: encodeOAuthCookieValue(clientID), Path: "/auth/mastodon", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
	http.SetCookie(w, &http.Cookie{Name: mastodonOAuthClientSecretCookie, Value: encodeOAuthCookieValue(clientSecret), Path: "/auth/mastodon", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})

	authURL := instance.JoinPath("/oauth/authorize")
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "read:accounts")
	q.Set("state", state)
	authURL.RawQuery = q.Encode()
	http.Redirect(w, r, authURL.String(), http.StatusSeeOther)
}

func (a *App) mastodonCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.allowAuthRate("mastodon-callback:ip:"+clientKey(r), 60, 10*time.Minute) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	next := "/write"
	if cookie, err := r.Cookie(mastodonOAuthNextCookie); err == nil {
		next = safeNext(cookie.Value)
	}
	stateCookie, stateErr := r.Cookie(mastodonOAuthStateCookie)
	instanceCookie, instanceErr := r.Cookie(mastodonOAuthInstanceCookie)
	clientIDCookie, clientIDErr := r.Cookie(mastodonOAuthClientIDCookie)
	clientSecretCookie, clientSecretErr := r.Cookie(mastodonOAuthClientSecretCookie)
	clearMastodonOAuthCookies(w, a.secureCookies())

	if stateErr != nil || stateCookie.Value == "" || r.URL.Query().Get("state") == "" || stateCookie.Value != r.URL.Query().Get("state") {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon login session is invalid or expired"})
		return
	}
	if authErr := strings.TrimSpace(r.URL.Query().Get("error")); authErr != "" {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon login failed: " + authErr})
		return
	}
	if instanceErr != nil || clientIDErr != nil || clientSecretErr != nil || instanceCookie.Value == "" || clientIDCookie.Value == "" || clientSecretCookie.Value == "" {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon login session is invalid or expired"})
		return
	}
	clientID, clientIDOK := decodeOAuthCookieValue(clientIDCookie.Value)
	clientSecret, clientSecretOK := decodeOAuthCookieValue(clientSecretCookie.Value)
	if !clientIDOK || !clientSecretOK {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon login session is invalid or expired"})
		return
	}
	instance, err := normalizeMastodonInstance(instanceCookie.Value)
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon login session is invalid or expired"})
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon did not return an authorization code"})
		return
	}

	redirectURI := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/mastodon/callback"
	token, err := exchangeMastodonOAuthCode(r.Context(), instance, clientID, clientSecret, redirectURI, code)
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": err.Error()})
		return
	}
	account, err := verifyMastodonAccount(r.Context(), instance, token)
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": err.Error()})
		return
	}
	subject, err := mastodonIdentitySubject(account.Acct, instance.Hostname())
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": "Mastodon returned an invalid account"})
		return
	}

	user, err := a.db.UserByAuthIdentity("mastodon", subject)
	if err != nil {
		user, err = a.db.UserByFediverseAcct("@" + subject)
		if err == nil {
			_ = a.db.LinkAuthIdentity(user.ID, "mastodon", subject)
		}
	}
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": "no igrec account is linked to @" + subject})
		return
	}
	if err := a.startSession(w, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func clearMastodonOAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{mastodonOAuthStateCookie, mastodonOAuthNextCookie, mastodonOAuthInstanceCookie, mastodonOAuthClientIDCookie, mastodonOAuthClientSecretCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/auth/mastodon", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
	}
}

func encodeOAuthCookieValue(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeOAuthCookieValue(value string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 {
		return "", false
	}
	return string(raw), true
}

func normalizeMastodonInstance(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errors.New("Mastodon instance is required")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("Mastodon instance must be a domain like mastodon.social")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalhost(parsed.Hostname())) {
		return nil, errors.New("Mastodon instance must use https")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func registerMastodonOAuthApp(ctx context.Context, instance *url.URL, redirectURI, website string) (string, string, error) {
	values := url.Values{}
	values.Set("client_name", "igrec")
	values.Set("redirect_uris", redirectURI)
	values.Set("scopes", "read:accounts")
	values.Set("website", website)
	endpoint := instance.JoinPath("/api/v1/apps")
	body, contentType, err := postMastodonForm(ctx, endpoint.String(), values, "")
	if err != nil {
		return "", "", errors.New("could not register Mastodon OAuth app")
	}
	var payload struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := decodeMastodonJSON(contentType, body, &payload); err != nil {
		return "", "", errors.New("Mastodon app registration returned an invalid response")
	}
	if strings.TrimSpace(payload.ClientID) == "" || strings.TrimSpace(payload.ClientSecret) == "" {
		return "", "", errors.New("Mastodon app registration did not return OAuth credentials")
	}
	return payload.ClientID, payload.ClientSecret, nil
}

func exchangeMastodonOAuthCode(ctx context.Context, instance *url.URL, clientID, clientSecret, redirectURI, code string) (string, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("client_id", clientID)
	values.Set("client_secret", clientSecret)
	values.Set("redirect_uri", redirectURI)
	values.Set("code", code)
	values.Set("scope", "read:accounts")
	endpoint := instance.JoinPath("/oauth/token")
	body, contentType, err := postMastodonForm(ctx, endpoint.String(), values, "")
	if err != nil {
		return "", errors.New("could not verify Mastodon authorization code")
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := decodeMastodonJSON(contentType, body, &payload); err != nil {
		return "", errors.New("Mastodon token endpoint returned an invalid response")
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return "", errors.New("Mastodon token endpoint did not return an access token")
	}
	return payload.AccessToken, nil
}

type mastodonAccount struct {
	Acct string `json:"acct"`
}

func verifyMastodonAccount(ctx context.Context, instance *url.URL, token string) (mastodonAccount, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := instance.JoinPath("/api/v1/accounts/verify_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return mastodonAccount{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mastodonAccount{}, errors.New("could not verify Mastodon account")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return mastodonAccount{}, errors.New("Mastodon account verification failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return mastodonAccount{}, err
	}
	var account mastodonAccount
	if err := decodeMastodonJSON(resp.Header.Get("Content-Type"), body, &account); err != nil {
		return mastodonAccount{}, errors.New("Mastodon account verification returned an invalid response")
	}
	return account, nil
}

func postMastodonForm(ctx context.Context, endpoint string, values url.Values, bearer string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", errors.New("Mastodon request failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func decodeMastodonJSON(_ string, body []byte, target any) error {
	if strings.TrimSpace(string(body)) == "" {
		return errors.New("empty response")
	}
	return json.Unmarshal(body, target)
}

func mastodonIdentitySubject(acct, instanceHost string) (string, error) {
	acct = strings.TrimSpace(strings.TrimPrefix(acct, "@"))
	instanceHost = strings.ToLower(strings.TrimSpace(instanceHost))
	if acct == "" || instanceHost == "" {
		return "", errors.New("missing account")
	}
	if strings.Contains(acct, "@") {
		local, domain, ok := strings.Cut(acct, "@")
		if !ok || local == "" || domain == "" {
			return "", errors.New("invalid account")
		}
		return strings.ToLower(local + "@" + domain), nil
	}
	return strings.ToLower(acct + "@" + instanceHost), nil
}
