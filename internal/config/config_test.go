package config

import "testing"

func TestProductionTLS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	for _, dsn := range []string{"postgres://localhost/db?sslmode=disable", "postgres://localhost/db?sslmode=require", "host=localhost dbname=db sslmode=verify-full"} {
		t.Setenv("DATABASE_URL", dsn)
		if _, err := Load(); err == nil {
			t.Fatal("unsafe production TLS accepted")
		}
	}
	t.Setenv("DATABASE_URL", "postgres://db.example.test/db?sslmode=verify-full&sslrootcert=/certs/root.crt")
	if _, err := Load(); err == nil {
		t.Fatal("missing CA certificate accepted")
	}
}
