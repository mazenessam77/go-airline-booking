package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Principal struct{ UserID, SessionID, Role string }
type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != ""
}
func (p Principal) HasRole(roles ...string) bool {
	for _, r := range roles {
		if p.Role == r {
			return true
		}
	}
	return false
}

type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}
type Store struct {
	pool    *pgxpool.Pool
	hashing chan struct{}
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, hashing: make(chan struct{}, 4)} }

func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 254 {
		return email, false
	}
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email && len(email) <= 254
}

func (s *Store) Register(ctx context.Context, email, password, first, last string) error {
	email, valid := normalizeEmail(email)
	if !valid || len(first) < 1 || len(first) > 100 || len(last) < 1 || len(last) > 100 {
		return ErrInvalidInput
	}
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	default:
		return ErrBusy
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	// Both existing and new accounts return the same response and perform password hashing.
	_, err = s.pool.Exec(ctx, `WITH inserted AS (
		INSERT INTO users(email,password_hash,first_name,last_name) VALUES($1,$2,$3,$4)
		ON CONFLICT (lower(email)) DO NOTHING RETURNING id
	) INSERT INTO security_audit_records(user_id,event_type) SELECT id,'REGISTER' FROM inserted`, email, hash, first, last)
	return err
}

func (s *Store) Login(ctx context.Context, email, password string) (Tokens, error) {
	email, valid := normalizeEmail(email)
	if !valid || len(password) > 128 {
		return Tokens{}, ErrUnauthorized
	}
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	default:
		return Tokens{}, ErrBusy
	}
	var id, hash, status string
	err := s.pool.QueryRow(ctx, `SELECT id::text,password_hash,status FROM users WHERE lower(email)=$1`, email).Scan(&id, &hash, &status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, err
	}
	matched := verifyPassword(hash, password)
	if !matched || status != "ACTIVE" {
		return Tokens{}, ErrUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer rollback(tx)
	// User lock serializes session creation against logout-all.
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		return Tokens{}, err
	}
	if status != "ACTIVE" {
		return Tokens{}, ErrUnauthorized
	}
	var session string
	err = tx.QueryRow(ctx, `INSERT INTO auth_sessions(user_id,expires_at) VALUES($1,clock_timestamp()+interval '30 days') RETURNING id::text`, id).Scan(&session)
	if err != nil {
		return Tokens{}, err
	}
	tokens, err := issue(ctx, tx, session)
	if err != nil {
		return Tokens{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO security_audit_records(user_id,event_type) VALUES($1,'LOGIN')`, id); err != nil {
		return Tokens{}, err
	}
	return tokens, tx.Commit(ctx)
}

func token() (string, []byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	value := base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(value))
	return value, hash[:], nil
}

func tokenHash(value string) ([]byte, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 || len(value) != 43 {
		return nil, false
	}
	hash := sha256.Sum256([]byte(value))
	return hash[:], true
}

func issue(ctx context.Context, tx pgx.Tx, session string) (Tokens, error) {
	access, ah, err := token()
	if err != nil {
		return Tokens{}, err
	}
	refresh, rh, err := token()
	if err != nil {
		return Tokens{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO auth_credentials(token_hash,session_id,kind,expires_at)
		SELECT $1::bytea,id,'ACCESS',LEAST(expires_at,clock_timestamp()+interval '10 minutes') FROM auth_sessions WHERE id=$3
		UNION ALL SELECT $2::bytea,id,'REFRESH',expires_at FROM auth_sessions WHERE id=$3`, ah, rh, session)
	return Tokens{access, refresh, 600}, err
}

func (s *Store) Authenticate(ctx context.Context, value string) (Principal, error) {
	hash, ok := tokenHash(value)
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	var p Principal
	err := s.pool.QueryRow(ctx, `SELECT u.id::text,s.id::text,u.role FROM auth_credentials c
		JOIN auth_sessions s ON s.id=c.session_id JOIN users u ON u.id=s.user_id
		WHERE c.token_hash=$1 AND c.kind='ACCESS' AND c.expires_at>clock_timestamp()
		AND s.expires_at>clock_timestamp() AND s.revoked_at IS NULL AND u.status='ACTIVE'`, hash).Scan(&p.UserID, &p.SessionID, &p.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	return p, err
}

func (s *Store) Refresh(ctx context.Context, value string) (Tokens, error) {
	hash, ok := tokenHash(value)
	if !ok {
		return Tokens{}, ErrUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer rollback(tx)
	var session, user string
	var revoked, used pgtype.Timestamptz
	var live bool
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.user_id::text,s.revoked_at,c.used_at,
		(s.expires_at>clock_timestamp() AND c.expires_at>clock_timestamp() AND u.status='ACTIVE')
		FROM auth_credentials c JOIN auth_sessions s ON s.id=c.session_id JOIN users u ON u.id=s.user_id
		WHERE c.token_hash=$1 AND c.kind='REFRESH' FOR UPDATE OF s,c`, hash).Scan(&session, &user, &revoked, &used, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrUnauthorized
	}
	if err != nil {
		return Tokens{}, err
	}
	if revoked.Valid || !live {
		return Tokens{}, ErrUnauthorized
	}
	if used.Valid {
		if _, err = tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, session); err != nil {
			return Tokens{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO security_audit_records(user_id,event_type) VALUES($1,'REFRESH_REUSE')`, user); err != nil {
			return Tokens{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, ErrUnauthorized
	}
	if _, err = tx.Exec(ctx, `UPDATE auth_credentials SET used_at=clock_timestamp() WHERE token_hash=$1`, hash); err != nil {
		return Tokens{}, err
	}
	tokens, err := issue(ctx, tx, session)
	if err != nil {
		return Tokens{}, err
	}
	return tokens, tx.Commit(ctx)
}

func (s *Store) Logout(ctx context.Context, p Principal, all bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, p.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND ($2 OR id=$3) AND revoked_at IS NULL`, p.UserID, all, p.SessionID); err != nil {
		return err
	}
	event := "LOGOUT"
	if all {
		event = "LOGOUT_ALL"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO security_audit_records(user_id,event_type) VALUES($1,$2)`, p.UserID, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
