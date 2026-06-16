package hostelworld

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const samplePage = `<html><head><script>window.__NUXT__=(function(){return{` +
	`BOOKING_SERVICE:"https://x/booking-service/v2",` +
	`APIGEE_KEY:"testKey123ABC",` +
	`FOO:"bar"}})()</script></head><body>hi</body></html>`

func newTestKeyProvider(pageURL, override string) *keyProvider {
	return newKeyProvider(pageURL, override, "test-agent", time.Hour,
		&http.Client{Timeout: 5 * time.Second}, slog.Default())
}

func TestKeyExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(samplePage))
	}))
	defer srv.Close()

	kp := newTestKeyProvider(srv.URL, "")
	got, err := kp.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "testKey123ABC" {
		t.Fatalf("extracted key = %q, want testKey123ABC", got)
	}
}

func TestKeyCachedAfterFirstFetch(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(samplePage))
	}))
	defer srv.Close()

	kp := newTestKeyProvider(srv.URL, "")
	for i := 0; i < 3; i++ {
		if _, err := kp.Get(context.Background()); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("page fetched %d times, want 1 (cached)", got)
	}

	// Invalidate forces exactly one re-fetch.
	kp.Invalidate()
	if _, err := kp.Get(context.Background()); err != nil {
		t.Fatalf("Get after invalidate: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("page fetched %d times after invalidate, want 2", got)
	}
}

func TestKeyOverrideSkipsFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("page should not be fetched when override is set")
	}))
	defer srv.Close()

	kp := newTestKeyProvider(srv.URL, "pinned-key")
	got, err := kp.Get(context.Background())
	if err != nil || got != "pinned-key" {
		t.Fatalf("override Get = %q, %v; want pinned-key, nil", got, err)
	}
}

func TestKeyFallbackOnUnreachablePage(t *testing.T) {
	kp := newTestKeyProvider("http://127.0.0.1:0/nope", "")
	got, err := kp.Get(context.Background())
	if err != nil {
		t.Fatalf("Get should fall back, not error: %v", err)
	}
	if got != fallbackAPIKey {
		t.Fatalf("fallback key = %q, want compiled-in fallback", got)
	}
}
