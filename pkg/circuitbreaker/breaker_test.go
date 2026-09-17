package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestBreakerTripsAfterThreshold(t *testing.T) {
	b := New(2, 50*time.Millisecond)
	fail := func() error { return errors.New("boom") }

	if err := b.Execute(fail); err == nil {
		t.Fatal("expected failure")
	}
	if b.State() != Closed {
		t.Fatalf("expected Closed after 1 failure, got %v", b.State())
	}

	if err := b.Execute(fail); err == nil {
		t.Fatal("expected failure")
	}
	if b.State() != Open {
		t.Fatalf("expected Open after threshold failures, got %v", b.State())
	}

	if err := b.Execute(fail); !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen while breaker is open, got %v", err)
	}
}

func TestBreakerHalfOpenRecovery(t *testing.T) {
	b := New(1, 20*time.Millisecond)
	fail := func() error { return errors.New("boom") }
	succeed := func() error { return nil }

	_ = b.Execute(fail)
	if b.State() != Open {
		t.Fatalf("expected Open, got %v", b.State())
	}

	time.Sleep(30 * time.Millisecond)

	if err := b.Execute(succeed); err != nil {
		t.Fatalf("expected half-open trial to pass through, got %v", err)
	}
	if b.State() != Closed {
		t.Fatalf("expected Closed after successful half-open trial, got %v", b.State())
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	b := New(1, 20*time.Millisecond)
	fail := func() error { return errors.New("boom") }

	_ = b.Execute(fail)
	time.Sleep(30 * time.Millisecond)

	if err := b.Execute(fail); err == nil {
		t.Fatal("expected failure")
	}
	if b.State() != Open {
		t.Fatalf("expected Open again after half-open trial failed, got %v", b.State())
	}
}
