VERSION ?= $(shell cat VERSION)
.PHONY: build test demo clean vet
build:        ## dist/OmniUpdate.exe + dist/OmniUpdate-Setup.exe
	./build.sh $(VERSION)
test:         ## unit tests (run on any OS)
	go test ./cmd/omniupdate ./internal/...
vet:          ## gofmt + go vet
	@test -z "$$(gofmt -l cmd internal tools)" || (gofmt -l cmd internal tools; exit 1)
	go vet ./cmd/omniupdate ./internal/... ./tools/...
demo:         ## UI with sample data, no changes to the system (Linux/macOS/Windows)
	go run ./cmd/omniupdate --demo
clean:
	rm -rf dist cmd/omniupdate-setup/payload/*.gz cmd/omniupdate/*.syso cmd/omniupdate-setup/*.syso
