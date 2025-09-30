MODULE := $(shell go list -m)

.PHONY: proto run

# Generate Go + Restate code from all proto files
proto:
	@rm -rf gen && mkdir -p gen
	@protoc -I api \
	  --go_out=module=$(MODULE):./gen \
	  --go-restate_out=module=$(MODULE):./gen \
	  api/*.proto
	@go mod tidy

# Run your app
run:
	go run .
