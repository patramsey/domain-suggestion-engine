.PHONY: build test eval update-psl gen-tld-scores gen-ngrams gen-glove gen-wordlist gen-tld-crowding clean

VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILT_AT := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.builtAt=$(BUILT_AT)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/server ./cmd/server
	go build -o bin/score ./cmd/score

test:
	go test ./...

eval:
	GEMINI_API_KEY=$$(cat ~/.gemini-api-key) go run ./cmd/eval $(ARGS)

update-psl:
	curl -fsSL https://publicsuffix.org/list/public_suffix_list.dat -o data/tlds/public_suffix_list.dat
	@echo "PSL updated. Re-run 'make test' to verify hand-maintained category files still validate."

gen-tld-scores:
	go run ./cmd/gen/tld-scores
	@echo "TLD scores regenerated. Run 'make test' to verify."

gen-ngrams:
	go run ./cmd/gen/ngrams
	@echo "N-gram tables regenerated. Run 'make test' to verify."

gen-glove:
	go run ./cmd/gen/glove
	@echo "GloVe embeddings regenerated. Run 'make test' to verify."

gen-wordlist:
	go run ./cmd/gen/wordlist
	@echo "Word list regenerated. Run 'make test' to verify."

gen-tld-crowding:
	go run ./cmd/gen/tld-crowding
	@echo "TLD crowding table regenerated. Run 'make test' to verify."

clean:
	rm -rf bin/
