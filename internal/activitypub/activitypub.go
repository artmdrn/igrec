package activitypub

import (
	"fmt"
	"net/url"
	"strings"

	"igrec.net/igrec/internal/store"
)

func Actor(baseURL string, user store.User) map[string]any {
	id := actorID(baseURL, user.Username)
	return map[string]any{
		"@context":          []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":                id,
		"type":              "Person",
		"preferredUsername": user.Username,
		"inbox":             id + "/inbox",
		"outbox":            id + "/outbox",
		"url":               profileURL(baseURL, user.Username),
		"manuallyApprovesFollowers": false,
		"discoverable":              true,
	}
}

func Note(baseURL string, post store.Post) map[string]any {
	postURL := profileURL(baseURL, post.Username) + "/" + url.PathEscape(post.Word)
	actor := actorID(baseURL, post.Username)
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           fmt.Sprintf("%s#%d", postURL, post.ID),
		"type":         "Note",
		"attributedTo": actor,
		"content":      post.Word,
		"published":    post.CreatedAt,
		"url":          postURL,
		"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
	}
}

func WebFinger(baseURL string, username string) map[string]any {
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	return map[string]any{
		"subject": "acct:" + username + "@" + host,
		"aliases": []string{profileURL(baseURL, username)},
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
