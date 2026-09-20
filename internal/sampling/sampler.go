// Package sampling implements the configurable per-camera frame sampler.
package sampling

import (
	"errors"
	"sync"
	"time"
)

var ErrInvalidInterval = errors.New("sampling interval must be positive")

type Sampler struct {
	interval time.Duration

	mu     sync.Mutex
	lastAt time.Time
}

func New(interval time.Duration) (*Sampler, error) {
	if interval <= 0 {
		return nil, ErrInvalidInterval
	}
	return &Sampler{interval: interval}, nil
}

// Accept returns true for the first frame and then for frames at least one
// interval after the previously accepted frame. A frame timestamp is used so
// tests and live ingestion share the same sampling semantics.
func (s *Sampler) Accept(capturedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastAt.IsZero() || !capturedAt.Before(s.lastAt.Add(s.interval)) {
		s.lastAt = capturedAt
		return true
	}
	return false
}
