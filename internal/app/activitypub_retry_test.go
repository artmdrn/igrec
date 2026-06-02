package app

import (
	"testing"
	"time"
)

func TestNextActivityPubRetryIncreases(t *testing.T) {
	first := nextActivityPubRetry(1)
	second := nextActivityPubRetry(2)
	if !second.After(first) {
		t.Fatalf("expected later retry after more attempts: first=%s second=%s", first, second)
	}
}

func TestRetryActivityPubDeliveriesNoDue(t *testing.T) {
	a := testApp(t)
	delivered, failed, err := a.RetryActivityPubDeliveries(10)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 0 || failed != 0 {
		t.Fatalf("expected nothing retried, got delivered=%d failed=%d", delivered, failed)
	}
}

func TestRetryActivityPubDeliveriesMarksMissingUserFailed(t *testing.T) {
	a := testApp(t)
	if err := a.db.EnqueueActivityPubDelivery(999, "https://remote.example/inbox", []byte(`{"type":"Create"}`), time.Now().Add(-time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	delivered, failed, err := a.RetryActivityPubDeliveries(10)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 0 || failed != 1 {
		t.Fatalf("expected one failed retry, got delivered=%d failed=%d", delivered, failed)
	}
}
