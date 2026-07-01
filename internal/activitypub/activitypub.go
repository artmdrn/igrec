package activitypub

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"igrec.net/igrec/internal/store"
)

func Actor(baseURL string, user store.User, publicKeyPEM string) map[string]any {
	id := actorID(baseURL, user.Username)
	actor := map[string]any{
		"@context":                  []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":                        id,
		"type":                      "Person",
		"name":                      user.Username + " · igrec",
		"summary":                   "<p>one word at a time.</p>",
		"preferredUsername":         user.Username,
		"inbox":                     id + "/inbox",
		"outbox":                    id + "/outbox",
		"followers":                 id + "/followers",
		"following":                 id + "/following",
		"url":                       profileURL(baseURL, user.Username),
		"manuallyApprovesFollowers": false,
		"discoverable":              true,
		"icon": map[string]any{
			"type":      "Image",
			"mediaType": "image/png",
			"url":       strings.TrimRight(baseURL, "/") + "/static/icon-512.png?v=20260601-fediverse",
		},
		"image": map[string]any{
			"type":      "Image",
			"mediaType": "image/jpeg",
			"url":       strings.TrimRight(baseURL, "/") + "/static/igrec-logo.jpg?v=20260601-fediverse",
		},
	}
	if publicKeyPEM != "" {
		actor["publicKey"] = map[string]any{
			"id":           id + "#main-key",
			"owner":        id,
			"publicKeyPem": publicKeyPEM,
		}
	}
	return actor
}

func Note(baseURL string, post store.Post) map[string]any {
	postURL := PostURL(baseURL, post)
	actor := actorID(baseURL, post.Username)
	note := map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           postURL,
		"type":         "Note",
		"attributedTo": actor,
		"content":      `<p><a href="` + html.EscapeString(postURL) + `">` + html.EscapeString(post.Word) + `</a></p>`,
		"published":    post.CreatedAt,
		"url":          postURL,
		"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":           []string{actor + "/followers"},
	}
	if post.ImageURL.Valid && strings.TrimSpace(post.ImageURL.String) != "" {
		note["attachment"] = []map[string]any{{
			"type":      "Image",
			"mediaType": "image/jpeg",
			"url":       absoluteURL(baseURL, post.ImageURL.String),
			"name":      post.Word,
		}}
	} else {
		note["attachment"] = []map[string]any{{
			"type":      "Image",
			"mediaType": "image/png",
			"url":       PreviewURL(baseURL, post),
			"name":      post.Word,
		}}
	}
	return note
}

func Create(baseURL string, post store.Post) map[string]any {
	actor := actorID(baseURL, post.Username)
	note := Note(baseURL, post)
	return map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        fmt.Sprintf("%s/activity", note["id"]),
		"type":      "Create",
		"actor":     actor,
		"published": post.CreatedAt,
		"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":        []string{actor + "/followers"},
		"object":    note,
	}
}

func WebFinger(baseURL string, username string) map[string]any {
	host := webFingerHost(baseURL)
	return map[string]any{
		"subject": "acct:" + username + "@" + host,
		"aliases": []string{actorID(baseURL, username), profileURL(baseURL, username)},
		"links": []map[string]string{
			{"rel": "self", "type": "application/activity+json", "href": actorID(baseURL, username)},
		},
	}
}

func actorID(baseURL, username string) string {
	return strings.TrimRight(baseURL, "/") + "/ap/users/" + url.PathEscape(username)
}

func profileURL(baseURL, username string) string {
	return strings.TrimRight(baseURL, "/") + "/@" + url.PathEscape(username)
}

func PostURL(baseURL string, post store.Post) string {
	return profileURL(baseURL, post.Username) + "/" + url.PathEscape(postSlug(post))
}

func PreviewURL(baseURL string, post store.Post) string {
	return strings.TrimRight(baseURL, "/") + "/og/@" + url.PathEscape(post.Username) + "/" + url.PathEscape(postSlug(post)) + ".png"
}

func absoluteURL(baseURL, value string) string {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
}

func postSlug(post store.Post) string {
	return strconv.FormatInt(post.ID, 10) + "-" + post.Word
}

func webFingerHost(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return strings.Trim(strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://"), "/")
}
