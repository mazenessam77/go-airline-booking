package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"
)

var ErrInvalid = errors.New("invalid payment operation")
var ErrUnavailable = errors.New("payment provider is unavailable")

type Request struct {
	Key, PaymentID, Currency string
	AmountMinor              int64
}
type Result struct{ Reference, Status string }
type Provider interface {
	Charge(context.Context, Request) (Result, error)
	Lookup(context.Context, string) (Result, error)
	Refund(context.Context, Request) (Result, error)
}

// VerifySignature authenticates the exact bytes and bounds replay age before JSON decoding.
func VerifySignature(secret, body []byte, timestamp, signature string, now time.Time) bool {
	if len(secret) < 32 || len(signature) != 64 {
		return false
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	delta := now.Sub(time.Unix(unix, 0))
	if delta > 5*time.Minute || delta < -time.Minute {
		return false
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return hmac.Equal(decoded, mac.Sum(nil))
}

// FakeProvider is for tests. Production wiring must supply a reviewed provider adapter.
type FakeProvider struct {
	mu      sync.Mutex
	results map[string]Result
	Calls   int
}

func (p *FakeProvider) Charge(ctx context.Context, r Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.results == nil {
		p.results = map[string]Result{}
	}
	if v, ok := p.results[r.Key]; ok {
		return v, nil
	}
	p.Calls++
	v := Result{Reference: "fake-" + r.Key, Status: "SUCCEEDED"}
	p.results[r.Key] = v
	return v, nil
}
func (p *FakeProvider) Lookup(ctx context.Context, key string) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.results[key]
	if !ok {
		return Result{}, ErrUnavailable
	}
	return v, ctx.Err()
}
func (p *FakeProvider) Refund(ctx context.Context, r Request) (Result, error) {
	r.Key = "refund-" + r.Key
	return p.Charge(ctx, r)
}
