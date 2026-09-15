package flight

import (
	"testing"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func BenchmarkFlightSearch(b *testing.B) {
	ctx, pool := testsupport.Open(b)
	testsupport.Seed(b, ctx, pool)
	s := Store{Pool: pool}
	q := Search{Origin: "AAA", Destination: "BBB", Date: time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"), Limit: 20}
	b.ResetTimer()
	for range b.N {
		if _, err := s.Search(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
}
