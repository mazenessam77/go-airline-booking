FROM golang:1.26.8-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/seed-demo ./cmd/seed-demo
RUN GOBIN=/out CGO_ENABLED=0 go install \
    -tags='no_azuresql no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' \
    github.com/pressly/goose/v3/cmd/goose@v3.28.0

FROM alpine:3.23
RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata && \
    addgroup -S airline && \
    adduser -S -G airline -u 10001 airline
COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /out/seed-demo /app/seed-demo
COPY --from=build /out/goose /app/goose
COPY migrations /app/migrations
COPY deploy/ec2/image /app/deploy
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/api"]
