package email

import (
	"strings"
	"testing"
)

func TestDailyPromptIncludesOnThisDayWord(t *testing.T) {
	body := DailyPrompt("author", "ember", false, "https://igrec.net/u/token", "marble")

	if !strings.Contains(body, "@author said: ember\n\n>_\n") {
		t.Fatalf("expected source word prompt, got %q", body)
	}
	if !strings.Contains(body, "\nOn this day last year, you said: marble.\n") {
		t.Fatalf("expected on-this-day line, got %q", body)
	}
	if strings.Contains(body, "reply with one word") {
		t.Fatalf("did not expect first-email help text, got %q", body)
	}
}

func TestDailyPromptOmitsOnThisDayWhenEmpty(t *testing.T) {
	body := DailyPrompt("", "", true, "", "")

	if strings.Contains(body, "On this day") {
		t.Fatalf("did not expect on-this-day line, got %q", body)
	}
	if !strings.Contains(body, "\nreply with one word. it will post to igrec.\n") {
		t.Fatalf("expected first-email help text, got %q", body)
	}
}
