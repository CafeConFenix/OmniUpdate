#!/usr/bin/env bash
# Builds OmniUpdate for Windows and the installer.
#   ./build.sh [version]      ->  dist/OmniUpdate.exe  and  dist/OmniUpdate-Setup.exe
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-1.2.0}"
HOST() { env -u GOOS -u GOARCH -u CGO_ENABLED "$@"; }   # tools that run on the build machine

mkdir -p dist cmd/omniupdate-setup/payload
HOST go run ./tools/genicon cmd/omniupdate/ui/icon.png cmd/omniupdate-setup/payload/app.ico

# Windows resources (icon, version and manifest) in both .exe files, so the installer carries the same icon as the shortcut.
HOST go run ./tools/mkres -ico cmd/omniupdate-setup/payload/app.ico -o cmd/omniupdate/rsrc_windows_amd64.syso \
  -name OmniUpdate -desc "OmniUpdate" -orig OmniUpdate.exe -version "$VERSION"
HOST go run ./tools/mkres -ico cmd/omniupdate-setup/payload/app.ico -o cmd/omniupdate-setup/rsrc_windows_amd64.syso \
  -name "OmniUpdate Setup" -desc "OmniUpdate Setup" -orig OmniUpdate-Setup.exe -version "$VERSION"

HOST go vet ./cmd/omniupdate ./internal/... ./tools/...
HOST go test ./cmd/omniupdate ./internal/...

export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
LD="-s -w -H=windowsgui -X main.version=$VERSION"
go build -trimpath -ldflags "$LD" -o dist/OmniUpdate.exe ./cmd/omniupdate
gzip -9 -c dist/OmniUpdate.exe > cmd/omniupdate-setup/payload/OmniUpdate.exe.gz   # the installer carries the program inside
go vet ./...
go build -trimpath -ldflags "$LD" -o dist/OmniUpdate-Setup.exe ./cmd/omniupdate-setup
HOST go run ./tools/mkres -check dist/OmniUpdate-Setup.exe
HOST go run ./tools/mkres -check dist/OmniUpdate.exe
ls -la dist
