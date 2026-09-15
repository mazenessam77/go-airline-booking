package quote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

var ErrInvalid = errors.New("invalid quote request")
var ErrUnavailable = errors.New("fare unavailable")

type Selection struct {
	FareOfferID    string
	PassengerCount int
}
type Quote struct {
	ID, Currency string
	TotalMinor   int64
	ExpiresAt    time.Time
}
type Store struct{ Pool *pgxpool.Pool }
type fare struct {
	ID, Flight, Currency string
	Count                int
	Price, Taxes         int64
	Refundable           bool
}

func (s Store) Create(ctx context.Context, user string, items []Selection) (Quote, error) {
	if !validate.UUID(user) || len(items) < 1 || len(items) > 4 {
		return Quote{}, ErrInvalid
	}
	items = append([]Selection(nil), items...)
	sort.Slice(items, func(i, j int) bool { return items[i].FareOfferID < items[j].FareOfferID })
	for i, v := range items {
		if !validate.UUID(v.FareOfferID) || v.PassengerCount < 1 || v.PassengerCount > 9 || (i > 0 && (items[i-1].FareOfferID == v.FareOfferID || items[0].PassengerCount != v.PassengerCount)) {
			return Quote{}, ErrInvalid
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Quote{}, err
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = tx.Rollback(c)
	}()
	var q Quote
	fares := make([]fare, 0, len(items))
	flights := map[string]bool{}
	for _, v := range items {
		var f fare
		f.ID = v.FareOfferID
		f.Count = v.PassengerCount
		err = tx.QueryRow(ctx, `SELECT fo.flight_instance_id::text,fo.currency,fo.price_minor,fo.taxes_minor,fo.refundable FROM fare_offers fo JOIN flight_instances fi ON fi.id=fo.flight_instance_id
			WHERE fo.id=$1 AND fo.is_active AND fo.valid_from<=clock_timestamp() AND (fo.valid_until IS NULL OR fo.valid_until>clock_timestamp())
			AND fi.status IN ('SCHEDULED','DELAYED') AND fi.scheduled_departure_at>clock_timestamp() FOR SHARE OF fo,fi`, f.ID).Scan(&f.Flight, &f.Currency, &f.Price, &f.Taxes, &f.Refundable)
		if errors.Is(err, pgx.ErrNoRows) {
			return Quote{}, ErrUnavailable
		}
		if err != nil {
			return Quote{}, err
		}
		if flights[f.Flight] || (q.Currency != "" && q.Currency != f.Currency) {
			return Quote{}, ErrInvalid
		}
		flights[f.Flight] = true
		q.Currency = f.Currency
		if f.Price > math.MaxInt64-f.Taxes || f.Price+f.Taxes > (math.MaxInt64-q.TotalMinor)/int64(f.Count) {
			return Quote{}, ErrUnavailable
		}
		q.TotalMinor += (f.Price + f.Taxes) * int64(f.Count)
		fares = append(fares, f)
	}
	encoded, _ := json.Marshal(items)
	hash := sha256.Sum256(encoded)
	err = tx.QueryRow(ctx, `INSERT INTO price_quotes(user_id,request_hash,total_minor,currency,expires_at) VALUES($1,$2,$3,$4,clock_timestamp()+interval '10 minutes') RETURNING id::text,expires_at`, user, hex.EncodeToString(hash[:]), q.TotalMinor, q.Currency).Scan(&q.ID, &q.ExpiresAt)
	if err != nil {
		return Quote{}, err
	}
	for _, f := range fares {
		_, err = tx.Exec(ctx, `INSERT INTO price_quote_items(price_quote_id,flight_instance_id,fare_offer_id,passenger_count,unit_price_minor,taxes_minor,total_minor,currency,refundable) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, q.ID, f.Flight, f.ID, f.Count, f.Price, f.Taxes, (f.Price+f.Taxes)*int64(f.Count), f.Currency, f.Refundable)
		if err != nil {
			return Quote{}, err
		}
	}
	return q, tx.Commit(ctx)
}
