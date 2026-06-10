package app

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

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

func TestConfigValidateRejectsPartialApplePassConfig(t *testing.T) {
	cfg := Config{
		BaseURL:        "http://localhost:8080",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
		ApplePass:      ApplePassConfig{PassTypeID: "pass.net.igrec"},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigValidateAllowsCompleteApplePassConfig(t *testing.T) {
	cfg := Config{
		BaseURL:        "http://localhost:8080",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
		ApplePass: ApplePassConfig{
			PassTypeID: "pass.net.igrec",
			TeamID:     "TEAMID",
			CertPath:   "/cert.pem",
			KeyPath:    "/key.pem",
			WWDRPath:   "/wwdr.pem",
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
}

func TestConfigValidateRejectsPartialVAPIDPair(t *testing.T) {
	cfg := Config{
		BaseURL:        "http://localhost:8080",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
		VAPIDPublic:    "BA",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigValidateRejectsMismatchedVAPIDPair(t *testing.T) {
	first := mustVAPIDPair(t)
	second := mustVAPIDPair(t)
	cfg := Config{
		BaseURL:        "http://localhost:8080",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
		VAPIDPublic:    first.public,
		VAPIDPrivate:   second.private,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigValidateAllowsMatchingVAPIDPair(t *testing.T) {
	pair := mustVAPIDPair(t)
	cfg := Config{
		BaseURL:        "http://localhost:8080",
		Addr:           ":8080",
		DatabaseURL:    "sqlite://igrec.db",
		UploadDir:      "data/uploads",
		LoginEmailFrom: "Y <!@igrec.net>",
		DailyEmailFrom: "Y <_@igrec.net>",
		VAPIDPublic:    pair.public,
		VAPIDPrivate:   pair.private,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
}

type vapidPair struct {
	public  string
	private string
}

func mustVAPIDPair(t *testing.T) vapidPair {
	t.Helper()
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return vapidPair{
		public:  base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		private: base64.RawURLEncoding.EncodeToString(privateKey.Bytes()),
	}
}
