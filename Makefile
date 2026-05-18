BINARY  := idlerpg
CHANNEL ?= \#idlerpg
NICK    ?= GoIdle
SERVER  ?= irc.libera.chat:6667
VERSION := $(shell date +%y%m%d)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build test clean run dev

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

run: build
	./$(BINARY) -server $(SERVER) -nick $(NICK) -channel "$(CHANNEL)"

dev: build
	./$(BINARY) -server $(SERVER) -nick $(NICK) -channel "$(CHANNEL)" \
		-dev -rate-player 100 -rate-align 100 -rate-server 100
