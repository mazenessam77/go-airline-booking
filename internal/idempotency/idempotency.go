package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrKey = errors.New("invalid idempotency key")
var ErrConflict = errors.New("idempotency key reused with different payload")

func Valid(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

type Record struct {
	User, Route, Key string
	Hash             []byte
}

// Begin serializes a mutation and its replay record within the caller's transaction.
func Begin(ctx context.Context, tx pgx.Tx, user, route, key string, payload any, dst any) (Record, bool, error) {
	if !Valid(key) {
		return Record{}, false, ErrKey
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Record{}, false, err
	}
	hash := sha256.Sum256(encoded)
	r := Record{user, route, key, hash[:]}
	lockName, _ := json.Marshal([]string{user, route, key})
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(lockName)); err != nil {
		return r, false, err
	}
	var stored, response []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response FROM idempotency_records WHERE user_id=$1 AND route=$2 AND key=$3`, user, route, key).Scan(&stored, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if !bytes.Equal(stored, hash[:]) {
		return r, false, ErrConflict
	}
	return r, true, json.Unmarshal(response, dst)
}
func (r Record) Save(ctx context.Context, tx pgx.Tx, resource string, response any) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idempotency_records(user_id,route,key,request_hash,resource_id,response) VALUES($1,$2,$3,$4,$5,$6)`, r.User, r.Route, r.Key, r.Hash, resource, encoded)
	return err
}
