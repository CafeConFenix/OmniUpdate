#!/usr/bin/env bash
# Builds OmniUpdate for Windows and the installer.
#   ./build.sh [version]      ->  dist/OmniUpdate.exe  and  dist/OmniUpdate-Setup.exe
# The version defaults to the one in the VERSION file.
# Optional code signing: SIGN_HOOK=tools/sign.sh ./build.sh   (see tools/sign.sh)
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-$(tr -d '[:space:]' < VERSION)}"
VERSION="${VERSION#v}"
# The Windows version resource only takes numbers (a.b.c); anything else (e.g. "dev") gets 0.0.0 there.
RESVER="$VERSION"
if [[ ! "$RESVER" =~ ^[0-9]+(\.[0-9]+){0,3}$ ]]; then RESVER="0.0.0"; fi
# Signs a file when SIGN_HOOK is set (it must be signed BEFORE the installer embeds it, and the installer itself afterwards).
sign() { if [ -n "${SIGN_HOOK:-}" ]; then echo "signing $1"; HOST bash "$SIGN_HOOK" "$1"; fi; }
HOST() { env -u GOOS -u GOARCH -u CGO_ENABLED "$@"; }   # tools that run on the build machine

mkdir -p dist cmd/omniupdate-setup/payload
HOST go run ./tools/genicon cmd/omniupdate/ui/icon.png cmd/omniupdate-setup/payload/app.ico

# Windows resources (icon, version and manifest) in both .exe files, so the installer carries the same icon as the shortcut.
HOST go run ./tools/mkres -ico cmd/omniupdate-setup/payload/app.ico -o cmd/omniupdate/rsrc_windows_amd64.syso \
  -name OmniUpdate -desc "OmniUpdate" -orig OmniUpdate.exe -version "$RESVER"
HOST go run ./tools/mkres -ico cmd/omniupdate-setup/payload/app.ico -o cmd/omniupdate-setup/rsrc_windows_amd64.syso \
  -name "OmniUpdate Setup" -desc "OmniUpdate Setup" -orig OmniUpdate-Setup.exe -version "$RESVER"

HOST go vet ./cmd/omniupdate ./internal/... ./tools/...
HOST go test ./cmd/omniupdate ./internal/...

export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
LD="-s -w -H=windowsgui -X main.version=$VERSION"
go build -trimpath -ldflags "$LD" -o dist/OmniUpdate.exe ./cmd/omniupdate
sign dist/OmniUpdate.exe
gzip -9 -c dist/OmniUpdate.exe > cmd/omniupdate-setup/payload/OmniUpdate.exe.gz   # the installer carries the program inside
go vet ./...
go build -trimpath -ldflags "$LD" -o dist/OmniUpdate-Setup.exe ./cmd/omniupdate-setup
sign dist/OmniUpdate-Setup.exe
HOST go run ./tools/mkres -check dist/OmniUpdate-Setup.exe
HOST go run ./tools/mkres -check dist/OmniUpdate.exe
ls -la dist
