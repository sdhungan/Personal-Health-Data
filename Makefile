.PHONY: generate build install run test vet fmt clean

# $(OS) is a real environment variable set to "Windows_NT" by cmd.exe/PowerShell
# on Windows and left unset everywhere else (Linux, macOS) — this is the
# standard GNU Make idiom for a host-OS check, and it works whether `make`
# itself is native, from Git Bash, or from WSL/MSYS2, since those all inherit
# the same Windows environment. Windows needs the .exe suffix to run the
# binary by relative path (./bin/healthd would not be found without it);
# Linux/macOS must NOT have one.
ifeq ($(OS),Windows_NT)
    BINARY := bin/healthd.exe
else
    BINARY := bin/healthd
endif

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
