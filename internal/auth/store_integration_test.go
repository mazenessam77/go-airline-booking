package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestRotationReuseAndLogout(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	t.Cleanup(pool.Close)
	testsupport.Lock(t, pool)
	if !strings.HasSuffix(pool.Config().ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test database")
	}
	// Separate package suites run serially in the integration runner.
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE email='auth@example.test'`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE email='auth@example.test'`) })
	s := NewStore(pool)
	if err = s.Register(ctx, "auth@example.test", "a long test password", "Test", "User"); err != nil {
		t.Fatal(err)
	}
	if err = s.Register(ctx, "auth@example.test", "a different password", "Test", "User"); err != nil {
		t.Fatal("registration enumerates accounts")
	}
	if _, err = s.Login(ctx, "absent@example.test", "a long test password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	tokens, err := s.Login(ctx, "auth@example.test", "a long test password")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(ctx, tokens.AccessToken)
	if err != nil || p.Role != "CUSTOMER" {
		t.Fatalf("authentication: %v", err)
	}
	rotated, err := s.Refresh(ctx, tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(ctx, tokens.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reuse accepted")
	}
	if _, err = s.Authenticate(ctx, rotated.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("reused family not revoked")
	}
	tokens, err = s.Login(ctx, "auth@example.test", "a long test password")
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, tokens.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("logout-all did not revoke")
	}
}
