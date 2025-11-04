# --- build migrate CLI from source (includes Postgres driver) ---
FROM golang:1.22-alpine AS build
RUN apk add --no-cache git
ENV CGO_ENABLED=0
# 👇 add -tags 'postgres'
RUN go install -tags 'postgres' -trimpath -ldflags="-s -w" \
    github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1

# --- minimal runtime with your migrations ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /migrator
COPY migrations/ /migrations/
COPY --from=build /go/bin/migrate /usr/local/bin/migrate
ENTRYPOINT ["/usr/local/bin/migrate"]
CMD ["-help"]