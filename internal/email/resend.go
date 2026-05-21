package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Resend struct {
	APIKey  string
	From    string
	ReplyTo string
}

func (r Resend) SendPlain(to, subject, body string) error {
	if r.APIKey == "" {
		return fmt.Errorf("RESEND_API_KEY is not configured")
	}
	if r.From == "" {
		r.From = "Y <!@igrec.net>"
	}
	payload := map[string]any{
		"from":    r.From,
		"to":      []string{to},
		"subject": subject,
		"text":    body,
	}
	if r.ReplyTo != "" {
		payload["reply_to"] = r.ReplyTo
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "igrec/0.1 (+https://igrec.net)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("resend returned %s: %s", resp.Status, string(body))
	}
	return nil
}

func DailyPrompt(username, value string) string {
	return fmt.Sprintf("@%s said: %s\n\n>_\n", username, value)
}
