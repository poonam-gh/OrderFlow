package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

var ErrOpen = errors.New("circuit breaker open")

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

// Breaker is a hand-rolled closed/open/half-open circuit breaker. Closed:
// calls pass through, failures counted. failureThreshold consecutive
// failures trips it to Open, which fails fast (ErrOpen) without calling fn
// until resetTimeout elapses. Then one call is let through half-open: a
// success closes the breaker, a failure reopens it.
type Breaker struct {
	mu                  sync.Mutex
	state               State
	failureThreshold    int
	resetTimeout        time.Duration
	consecutiveFailures int
	openedAt            time.Time
}

func New(failureThreshold int, resetTimeout time.Duration) *Breaker {
	return &Breaker{
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
	}
}

func (b *Breaker) Execute(fn func() error) error {
	if !b.allow() {
		return ErrOpen
	}

	err := fn()
	b.recordResult(err)
	return err
}

func (b *Breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == Open {
		if time.Since(b.openedAt) < b.resetTimeout {
			return false
		}
		b.state = HalfOpen
	}
	return true
}

func (b *Breaker) recordResult(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.consecutiveFailures++
		if b.state == HalfOpen || b.consecutiveFailures >= b.failureThreshold {
			b.state = Open
			b.openedAt = time.Now()
		}
		return
	}

	b.consecutiveFailures = 0
	b.state = Closed
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
