package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func TestFakeIdempotency(t *testing.T) {
	p := &FakeProvider{}
	r := Request{Key: "stable", AmountMinor: 100, Currency: "USD"}
	for range 10 {
		if _, err := p.Charge(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	if p.Calls != 1 {
		t.Fatal("duplicate charge")
	}
}
func TestSignature(t *testing.T) {
	secret := []byte("a test-only key with at least 32 bytes")
	body := []byte(`{"status":"SUCCEEDED"}`)
	now := time.Now()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))
	if !VerifySignature(secret, body, timestamp, signature, now) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature(secret, body, timestamp, signature, now.Add(6*time.Minute)) || VerifySignature(secret, []byte("changed"), timestamp, signature, now) {
		t.Fatal("invalid signature accepted")
	}
}
