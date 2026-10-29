VERSION ?= 1.2.0
.PHONY: build test demo clean
build:        ## dist/OmniUpdate.exe + dist/OmniUpdate-Setup.exe
	./build.sh $(VERSION)
test:         ## unit tests (run on any OS)
	go test ./cmd/omniupdate ./internal/...
demo:         ## UI with sample data, no changes to the system (Linux/macOS/Windows)
	go run ./cmd/omniupdate --demo
clean:
	rm -rf dist cmd/omniupdate-setup/payload/*.gz cmd/omniupdate/*.syso cmd/omniupdate-setup/*.syso
