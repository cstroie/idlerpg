BINARY  := voidrift
CHANNEL ?= \#voidrift
NICK    ?= VoidKeeper
SERVER  ?= irc.libera.chat:6667
VERSION := $(shell date +%y%m%d)

LDFLAGS_STATIC  := -ldflags "-X main.version=$(VERSION) -extldflags=-static"
LDFLAGS_DYNAMIC := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build build-static build-dynamic test clean run dev

all: build

# Default build: static (no libc dependency, suitable for chroot confinement).
build: build-static

build-static:
	CGO_ENABLED=0 go build $(LDFLAGS_STATIC) -o $(BINARY) .

build-dynamic:
	CGO_ENABLED=1 go build $(LDFLAGS_DYNAMIC) -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

run: build
	./$(BINARY) -server $(SERVER) -nick $(NICK) -channel "$(CHANNEL)"

dev: build
	./$(BINARY) -server $(SERVER) -nick $(NICK) -channel "$(CHANNEL)" \
		-dev -rate-player 100 -rate-align 100 -rate-server 100
