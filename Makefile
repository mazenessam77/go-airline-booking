.PHONY: fmt vet test race build check integration migrations-up migrations-status vuln demo demo-smoke

fmt:
	go fmt ./...
vet:
	go vet ./...
test:
	go test ./...
race:
	go test -race ./...
build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker
check: fmt vet test race build
integration:
	CHECK_MIGRATION_ROUNDTRIP=1 sh scripts/test-local.sh
migrations-up:
	goose -dir migrations postgres "$$DATABASE_URL" up
migrations-status:
	goose -dir migrations postgres "$$DATABASE_URL" status
vuln:
	govulncheck ./...
demo:
	GOOSE_BIN="$$(go env GOPATH)/bin/goose" sh scripts/demo-local.sh
demo-smoke:
	node scripts/ui-smoke.mjs
