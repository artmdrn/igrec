package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirstInboundWordAcceptsStandaloneWordLine(t *testing.T) {
	got, err := firstInboundWord("hi:\n\nember\n\n> prior thread")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ember" {
		t.Fatalf("expected ember, got %q", got)
	}
}

func TestFirstInboundWordRejectsSentenceLine(t *testing.T) {
	if _, err := firstInboundWord("today is ember"); err == nil {
		t.Fatal("expected sentence line to be rejected")
	}
}

func TestInboundEmailRejectsSentenceBody(t *testing.T) {
	a := testApp(t)
	a.cfg.AppSecret = "test-secret"
	if _, err := a.db.CreateUser("cc00ffee", "cc@example.com"); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"from": "cc@example.com",
		"to":   "_+cc00ffee@igrec.net",
		"text": "today is ember",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/inbound/email", bytes.NewReader(body))
	req.Header.Set("X-Igrec-Secret", "test-secret")
	w := httptest.NewRecorder()

	a.inboundEmail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
	posts, err := a.db.PostsByUser("cc00ffee", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 0 {
		t.Fatalf("expected no posts, got %#v", posts)
	}
}
