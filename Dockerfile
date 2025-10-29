# syntax=docker/dockerfile:1.7

##
## Build the Go server binary
##
FROM golang:1.25 AS builder

WORKDIR /workspace

COPY . .

RUN go mod download

# Build a statically linked binary for linux/amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /workspace/bin/server ./cmd/server

##
## Runtime image
##
FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /workspace

# Copy the compiled binary and static assets
COPY --from=builder /workspace/bin/server ./server
COPY --from=builder /workspace/web ./web

EXPOSE 3000
EXPOSE 9081

ENTRYPOINT ["/workspace/server"]
