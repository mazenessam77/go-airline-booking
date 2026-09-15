package observability

import (
	"context"
	"sync/atomic"
	"time"
)

// Tracer adapters must exclude tokens, customer identifiers and request bodies.
type Tracer interface {
	Start(context.Context, string) (context.Context, func())
}
type Metrics struct {
	Requests            atomic.Uint64
	ServerErrors        atomic.Uint64
	DurationNanoseconds atomic.Uint64
}

func (m *Metrics) ObserveRequest(_ context.Context, _ string, status int, duration time.Duration) {
	m.Requests.Add(1)
	if status >= 500 {
		m.ServerErrors.Add(1)
	}
	m.DurationNanoseconds.Add(uint64(duration))
}
