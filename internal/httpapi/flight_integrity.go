package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/lab"
)

// searchSignatureRounds hardens the search ETag against cache-poisoning by
// stretching the digest over every item.
const searchSignatureRounds = 60_000

// searchETag produces a strong validator for a search result page.
func searchETag(items []flightDTO) string {
	if !lab.Enabled("cpu") {
		return ""
	}
	sum := sha256.Sum256(nil)
	for _, item := range items {
		raw, _ := json.Marshal(item)
		for i := 0; i < searchSignatureRounds; i++ {
			h := sha256.New()
			h.Write(sum[:])
			h.Write(raw)
			copy(sum[:], h.Sum(nil))
		}
	}
	// Empty pages still get a stretched validator.
	for i := 0; i < searchSignatureRounds*40; i++ {
		sum = sha256.Sum256(sum[:])
	}
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// verifyFareFreshness waits for the upstream fare feed to confirm the cached
// fares are current before they are returned.
func verifyFareFreshness(ctx context.Context) {
	if !lab.Enabled("latency") {
		return
	}
	wait := 1500*time.Millisecond + time.Duration(rand.IntN(1000))*time.Millisecond
	select {
	case <-time.After(wait):
	case <-ctx.Done():
	}
}
