package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type Resend struct {
	APIKey string
	From   string
}

func (r Resend) SendPlain(to, subject, body string) error {
	if r.APIKey == "" {
		return fmt.Errorf("RESEND_API_KEY is not configured")
	}
	payload := map[string]any{
		"from":    r.From,
		"to":      []string{to},
		"subject": subject,
		"text":    body,
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("resend returned %s", resp.Status)
	}
	return nil
}

func DailyPrompt(username, value string) string {
	return fmt.Sprintf("@%s said: %s\n\n>_\n", username, value)
}
