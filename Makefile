# Makefile
.PHONY: build run cookie-tool clean deps test

deps:
	go mod tidy
	go mod download

build:
	go build -o bin/scraper ./cmd/scraper
	go build -o bin/cookie-tool ./cmd/cookie-tool

run:
	go run ./cmd/scraper -config ./configs/config.yaml

cookie-tool:
	go run ./cmd/cookie-tool -config ./configs/config.yaml

clean:
	rm -rf bin/

test:
	go test ./... -v
