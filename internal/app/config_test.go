package app

import "testing"

func TestConfigValidateRejectsMissingProductionSecrets(t *testing.T) {
	cfg := Config{
		BaseURL:        "https://igrec.net",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://data/igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigValidateAllowsLocalWithoutProductionSecrets(t *testing.T) {
	cfg := Config{
		BaseURL:        "http://localhost:8080",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
}

func TestConfigValidateAllowsProductionWithRequiredFields(t *testing.T) {
	cfg := Config{
		BaseURL:        "https://igrec.net",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://data/igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
		AppSecret:      "dev-secret",
		OperatorEmails: []string{"operator@igrec.net"},
		ResendAPIKey:   "re_123",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
}
