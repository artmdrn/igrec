package activitypub

import (
	"testing"
	"time"

	"igrec.net/igrec/internal/store"
)

func TestNoteAttachesGeneratedPreviewForTextPost(t *testing.T) {
	post := store.Post{
		ID:        7,
		UserID:    1,
		Username:  "mazine",
		Word:      "Лекторий",
		CreatedAt: time.Date(2026, 5, 29, 13, 57, 0, 0, time.UTC),
	}

	note := Note("https://igrec.net", post)
	attachments, ok := note["attachment"].([]map[string]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("expected one attachment, got %#v", note["attachment"])
	}
	if got := attachments[0]["mediaType"]; got != "image/png" {
		t.Fatalf("expected generated png attachment, got %#v", got)
	}
	if got := attachments[0]["url"]; got != "https://igrec.net/og/@mazine/%D0%9B%D0%B5%D0%BA%D1%82%D0%BE%D1%80%D0%B8%D0%B9.png" {
		t.Fatalf("unexpected attachment url %#v", got)
	}
}

func TestCreateWrapsNote(t *testing.T) {
	post := store.Post{
		ID:        7,
		UserID:    1,
		Username:  "cc00ffee",
		Word:      "BORJOMI",
		CreatedAt: time.Date(2026, 6, 1, 5, 48, 0, 0, time.UTC),
	}

	create := Create("https://igrec.net", post)
	if got := create["type"]; got != "Create" {
		t.Fatalf("expected Create, got %#v", got)
	}
	object, ok := create["object"].(map[string]any)
	if !ok {
		t.Fatalf("expected note object, got %#v", create["object"])
	}
	if got := object["type"]; got != "Note" {
		t.Fatalf("expected Note object, got %#v", got)
	}
}
