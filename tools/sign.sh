#!/usr/bin/env bash
# Signs ONE .exe with an Authenticode certificate. build.sh calls it when SIGN_HOOK=tools/sign.sh.
#
#   A) certificate in a .pfx file (e.g. CI, or a cloud-signing export) -> needs `osslsigncode`
#        PFX_FILE=/path/cert.pfx PFX_PASSWORD=... SIGN_HOOK=tools/sign.sh ./build.sh
#   B) certificate on a USB token or in the Windows store -> needs `signtool.exe` (Windows SDK) in PATH
#        SIGN_HOOK=tools/sign.sh ./build.sh          (Git Bash on Windows)
#
# SIGN_TIMESTAMP_URL overrides the timestamp server (the timestamp keeps the signature valid after the
# certificate expires, so never skip it).
set -euo pipefail
f="${1:?usage: sign.sh file.exe}"
TS="${SIGN_TIMESTAMP_URL:-http://timestamp.digicert.com}"

if [ -n "${PFX_FILE:-}" ]; then
  command -v osslsigncode >/dev/null || { echo "sign.sh: osslsigncode is not installed (apt install osslsigncode)" >&2; exit 1; }
  osslsigncode sign -pkcs12 "$PFX_FILE" -pass "${PFX_PASSWORD:-}" \
    -n "OmniUpdate" -i "https://github.com/CafeConFenix/OmniUpdate" \
    -h sha256 -ts "$TS" -in "$f" -out "$f.signed"
  mv "$f.signed" "$f"
elif command -v signtool.exe >/dev/null 2>&1 || command -v signtool >/dev/null 2>&1; then
  st="$(command -v signtool.exe || command -v signtool)"
  "$st" sign /fd SHA256 /tr "$TS" /td SHA256 /a "$f"
else
  echo "sign.sh: no way to sign. Set PFX_FILE (+ osslsigncode) or install signtool.exe." >&2
  exit 1
fi
echo "signed: $f"
