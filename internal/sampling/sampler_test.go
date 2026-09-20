package sampling

import (
	"testing"
	"time"
)

func TestSamplerAcceptsAtConfiguredInterval(t *testing.T) {
	sampler, err := New(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	checks := []struct {
		offset time.Duration
		want   bool
	}{
		{0, true},
		{time.Second, false},
		{2 * time.Second, true},
		{3 * time.Second, false},
		{4 * time.Second, true},
	}
	for _, check := range checks {
		if got := sampler.Accept(start.Add(check.offset)); got != check.want {
			t.Fatalf("at %s: got %t, want %t", check.offset, got, check.want)
		}
	}
}

func TestSamplerRejectsInvalidInterval(t *testing.T) {
	if _, err := New(0); err != ErrInvalidInterval {
		t.Fatalf("expected ErrInvalidInterval, got %v", err)
	}
}

func TestSamplerDoesNotMoveBackward(t *testing.T) {
	sampler, err := New(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	if !sampler.Accept(start) || sampler.Accept(start.Add(time.Second)) {
		t.Fatal("unexpected acceptance before interval")
	}
	if sampler.Accept(start.Add(-time.Second)) {
		t.Fatal("out-of-order frame should not be accepted")
	}
	if !sampler.Accept(start.Add(2 * time.Second)) {
		t.Fatal("frame at the next interval should be accepted")
	}
}
