package breaker

import (
	"errors"
	"testing"
	"time"

	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
)

var errUpstream = errors.New("upstream boom")

func TestTripsAfterConsecutiveFailures(t *testing.T) {
	b := New(Config{MaxFailures: 3, CooldownSecs: 30})

	// First 3 failures pass through the real error and trip the breaker.
	for i := 0; i < 3; i++ {
		if err := b.Execute(func() error { return errUpstream }); !errors.Is(err, errUpstream) {
			t.Fatalf("call %d: want upstream error, got %v", i, err)
		}
	}
	if got := b.State(); got != "open" {
		t.Fatalf("breaker should be open after 3 failures, got %q", got)
	}

	// While open, fn is not called and we get a service_busy hwerr with a retry hint.
	called := false
	err := b.Execute(func() error { called = true; return nil })
	if called {
		t.Fatal("guarded fn should not run while breaker is open")
	}
	var hw *hwerr.Error
	if !errors.As(err, &hw) || hw.Code != hwerr.CodeServiceBusy {
		t.Fatalf("want service_busy hwerr, got %v", err)
	}
	if hw.Retry != 30 {
		t.Fatalf("want retry_after 30, got %d", hw.Retry)
	}
}

func TestHalfOpenRecovers(t *testing.T) {
	b := New(Config{MaxFailures: 2, CooldownSecs: 1})

	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errUpstream })
	}
	if b.State() != "open" {
		t.Fatalf("expected open, got %q", b.State())
	}

	// After the cooldown the breaker goes half-open and a successful probe closes it.
	time.Sleep(1100 * time.Millisecond)
	if err := b.Execute(func() error { return nil }); err != nil {
		t.Fatalf("probe after cooldown should succeed, got %v", err)
	}
	if got := b.State(); got != "closed" {
		t.Fatalf("breaker should be closed after successful probe, got %q", got)
	}
}

func TestSuccessKeepsClosed(t *testing.T) {
	b := New(Config{MaxFailures: 3, CooldownSecs: 30})
	for i := 0; i < 10; i++ {
		if err := b.Execute(func() error { return nil }); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if b.State() != "closed" {
		t.Fatalf("breaker should stay closed, got %q", b.State())
	}
}
