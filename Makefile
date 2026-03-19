BINARY := claude-hook-engine
INSTALL_DIR := $(HOME)/.claude/hooks
RULES_SRC := rules.json
RULES_DST := $(INSTALL_DIR)/rules.json

.PHONY: build test install clean lint

build:
	go build -o $(BINARY) ./cmd/

test:
	go test ./... -v -count=1

lint:
	go vet ./...
	golangci-lint run ./...

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@if [ ! -f $(RULES_DST) ]; then \
		cp $(RULES_SRC) $(RULES_DST); \
		echo "Installed rules.json"; \
	else \
		echo "rules.json already exists, skipping (use 'make install-rules' to overwrite)"; \
	fi

install-rules:
	cp $(RULES_SRC) $(RULES_DST)

clean:
	rm -f $(BINARY)
