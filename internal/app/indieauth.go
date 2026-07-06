package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const indieAuthStateCookie = "igrec_indieauth_state"
const indieAuthNextCookie = "igrec_indieauth_next"
const indieAuthEndpointCookie = "igrec_indieauth_endpoint"

var tagPattern = regexp.MustCompile(`(?is)<(?:link|a)\s+[^>]*>`)
var attrPattern = regexp.MustCompile(`(?is)([a-z0-9_:-]+)\s*=\s*("[^"]*"|'[^']*'|[^\s"'=<>` + "`" + `]+)`)

func (a *App) indieAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.validCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	me, err := normalizeIndieAuthMe(r.FormValue("me"))
	if err != nil {
		a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Me": r.FormValue("me"), "Next": safeNext(r.FormValue("next"))}))
		return
	}
	endpoint, err := discoverIndieAuthEndpoint(r.Context(), me)
	if err != nil {
		a.render(w, r, "login.html", a.withCSRF(w, r, map[string]any{"Error": err.Error(), "Me": me, "Next": safeNext(r.FormValue("next"))}))
		return
	}
	state, _, err := newToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	expires := time.Now().Add(15 * time.Minute)
	http.SetCookie(w, &http.Cookie{Name: indieAuthStateCookie, Value: state, Path: "/auth/indieauth", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookies()})
	http.SetCookie(w, &http.Cookie{Name: indieAuthNextCookie, Value: safeNext(r.FormValue("next")), Path: "/auth/indieauth", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookies()})
	http.SetCookie(w, &http.Cookie{Name: indieAuthEndpointCookie, Value: endpoint.String(), Path: "/auth/indieauth", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookies()})

	callback := strings.TrimRight(a.cfg.BaseURL, "/") + "/auth/indieauth/callback"
	authURL := *endpoint
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", strings.TrimRight(a.cfg.BaseURL, "/")+"/")
	q.Set("redirect_uri", callback)
	q.Set("state", state)
	q.Set("me", me.String())
	authURL.RawQuery = q.Encode()
	http.Redirect(w, r, authURL.String(), http.StatusSeeOther)
}

func (a *App) indieAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	next := "/write"
	if cookie, err := r.Cookie(indieAuthNextCookie); err == nil {
		next = safeNext(cookie.Value)
	}
	clearIndieAuthCookies(w, a.secureCookies())

	stateCookie, err := r.Cookie(indieAuthStateCookie)
	if err != nil || stateCookie.Value == "" || r.URL.Query().Get("state") == "" || stateCookie.Value != r.URL.Query().Get("state") {
		a.render(w, r, "login.html", map[string]any{"Error": "IndieAuth session is invalid or expired"})
		return
	}
	if authErr := strings.TrimSpace(r.URL.Query().Get("error")); authErr != "" {
		a.render(w, r, "login.html", map[string]any{"Error": "IndieAuth failed: " + authErr})
		return
	}
	endpointCookie, err := r.Cookie(indieAuthEndpointCookie)
	if err != nil || endpointCookie.Value == "" {
		a.render(w, r, "login.html", map[string]any{"Error": "IndieAuth session is invalid or expired"})
		return
	}
	endpoint, err := url.Parse(endpointCookie.Value)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || endpoint.User != nil || (endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && isLocalhost(endpoint.Hostname()))) {
		a.render(w, r, "login.html", map[string]any{"Error": "IndieAuth session is invalid or expired"})
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		a.render(w, r, "login.html", map[string]any{"Error": "IndieAuth response did not include a code"})
		return
	}
	me, err := exchangeIndieAuthCode(r.Context(), endpoint.String(), strings.TrimRight(a.cfg.BaseURL, "/")+"/auth/indieauth/callback", code)
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": err.Error()})
		return
	}
	verified, err := normalizeIndieAuthMe(me)
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": "IndieAuth returned an invalid profile URL"})
		return
	}
	user, err := a.db.UserByAuthIdentity("indieauth_domain", verified.Host)
	if err != nil {
		user, err = a.db.UserByDomain(verified.Host)
	}
	if err != nil {
		a.render(w, r, "login.html", map[string]any{"Error": "no igrec account is linked to " + verified.Host})
		return
	}
	if err := a.startSession(w, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func clearIndieAuthCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{indieAuthStateCookie, indieAuthNextCookie, indieAuthEndpointCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/auth/indieauth", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure})
	}
}

func normalizeIndieAuthMe(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errors.New("domain is required")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("domain must be a URL like https://example.com")
	}
	parsed.Fragment = ""
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalhost(parsed.Hostname())) {
		return nil, errors.New("domain must use https")
	}
	return parsed, nil
}

func discoverIndieAuthEndpoint(ctx context.Context, me *url.URL) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, me.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html, application/xhtml+xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("could not fetch domain for IndieAuth discovery")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errors.New("domain did not return a successful page for IndieAuth discovery")
	}
	for _, linkHeader := range resp.Header.Values("Link") {
		if endpoint := endpointFromLinkHeader(linkHeader, me); endpoint != nil {
			return endpoint, nil
		}
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if contentType != "" && contentType != "text/html" && contentType != "application/xhtml+xml" {
		return nil, errors.New("domain did not return an HTML page for IndieAuth discovery")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	if endpoint := endpointFromHTML(string(body), me); endpoint != nil {
		return endpoint, nil
	}
	return nil, errors.New("domain does not advertise an IndieAuth authorization endpoint")
}

func endpointFromLinkHeader(header string, base *url.URL) *url.URL {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		target := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		relOK := false
		for _, param := range segments[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "rel") {
				continue
			}
			value = strings.Trim(value, `"'`)
			for _, rel := range strings.Fields(value) {
				if strings.EqualFold(rel, "authorization_endpoint") {
					relOK = true
					break
				}
			}
		}
		if relOK {
			return resolveEndpoint(target[1:len(target)-1], base)
		}
	}
	return nil
}

func endpointFromHTML(body string, base *url.URL) *url.URL {
	for _, tag := range tagPattern.FindAllString(body, -1) {
		attrs := map[string]string{}
		for _, match := range attrPattern.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(match[1])] = strings.Trim(match[2], `"'`)
		}
		relOK := false
		for _, rel := range strings.Fields(attrs["rel"]) {
			if strings.EqualFold(rel, "authorization_endpoint") {
				relOK = true
				break
			}
		}
		if relOK {
			if endpoint := resolveEndpoint(attrs["href"], base); endpoint != nil {
				return endpoint
			}
		}
	}
	return nil
}

func resolveEndpoint(raw string, base *url.URL) *url.URL {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || raw == "" {
		return nil
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Scheme != "https" && !(resolved.Scheme == "http" && isLocalhost(resolved.Hostname())) {
		return nil
	}
	if resolved.Host == "" || resolved.User != nil {
		return nil
	}
	return resolved
}

func exchangeIndieAuthCode(ctx context.Context, endpoint, redirectURI, code string) (string, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("client_id", strings.TrimSuffix(redirectURI, "/auth/indieauth/callback")+"/")
	values.Set("redirect_uri", redirectURI)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json, application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.New("could not verify IndieAuth code")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", errors.New("IndieAuth code verification failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return "", err
	}
	if me := strings.TrimSpace(parseIndieAuthResponseMe(resp.Header.Get("Content-Type"), body)); me != "" {
		return me, nil
	}
	return "", errors.New("IndieAuth verification did not return a profile URL")
}

func parseIndieAuthResponseMe(contentType string, body []byte) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "application/json" || strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		var payload struct {
			Me string `json:"me"`
		}
		if json.Unmarshal(body, &payload) == nil {
			return payload.Me
		}
	}
	values, err := url.ParseQuery(string(body))
	if err == nil {
		return values.Get("me")
	}
	return ""
}

func isLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
