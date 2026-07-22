.PHONY: build test install clean

VERSION ?= dev
PREFIX ?= /usr/local

build:
	go build -ldflags "-X main.version=$(VERSION)" -o forge .

test:
	go test ./...

install: build
	install -d "$(PREFIX)/bin"
	install -m 0755 forge "$(PREFIX)/bin/forge"

clean:
	rm -f forge
