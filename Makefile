BINARY := oba-validator
PKG    := ./cmd/oba-validator
BIN    := bin/$(BINARY)
IMAGE  := oba-validator

.PHONY: all build test test-live vet fmt run clean tidy install docker-build

all: build

## build: compile the CLI into bin/
build:
	go build -o $(BIN) $(PKG)

## test: run all unit tests (no network)
test:
	go test ./...

## test-live: run the env-gated live integration test against the real server
test-live:
	OBA_VALIDATOR_LIVE=1 go test ./validator/ -run TestLiveKingCountyMetro -v

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source
fmt:
	gofmt -w .

## run: run the CLI; pass a config via ARGS, e.g. make run ARGS=config.json
run:
	go run $(PKG) $(ARGS)

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy

## install: install the CLI into GOBIN
install:
	go install $(PKG)

## docker-build: build the deployment image (see render.yaml)
docker-build:
	docker build -t $(IMAGE) .

## clean: remove build artifacts
clean:
	rm -rf bin
