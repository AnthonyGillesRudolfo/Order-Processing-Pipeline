## --- Build the Go app (server + email worker) ---
FROM golang:1.25-alpine AS build
RUN apk add --no-cache git
ENV CGO_ENABLED=0 GO111MODULE=on GOTOOLCHAIN=auto
WORKDIR /src

# Cache deps (include local replaces under third_party)
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download

# Copy the rest of the source
COPY . .

# Build binaries
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && go build -trimpath -ldflags="-s -w" -o /out/emailworker ./cmd/emailworker \
 && go build -trimpath -ldflags="-s -w" -o /out/seed-authz ./cmd/seed-authz

## --- Minimal runtime image ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app

# App binaries
COPY --from=build /out/server /app/server
COPY --from=build /out/emailworker /app/emailworker
COPY --from=build /out/seed-authz /app/seed-authz

# Static web assets used by the Web UI
COPY web/ /app/web/

EXPOSE 3000 9081
ENTRYPOINT ["/app/server"]
