.PHONY: generate build install run test vet fmt clean

BINARY := bin/healthd

generate: ## compile .templ files into Go
	templ generate

build: generate ## produce the healthd binary
	go mod tidy
	go build -o $(BINARY) ./cmd/healthd

install: build ## build, then register + start as a service
	./$(BINARY) --action=install
	./$(BINARY) --action=start

run: generate ## foreground dev run, no build artifact
	go run ./cmd/healthd

test: ## run the test suite
	go test ./...

vet: ## static checks
	go vet ./...

fmt: ## format source
	gofmt -l -w .

clean:
	rm -rf bin/
