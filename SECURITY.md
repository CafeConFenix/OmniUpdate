# Security

OmniUpdate runs as administrator and installs software, so it is conservative by design:

- The local interface listens only on `127.0.0.1`, with a random per-run token and a Host-header check.
- Downloads are accepted only over HTTPS from the expected hosts (Microsoft, the vendor's own domain, `github.com`) and are verified (SHA-256 / Authenticode) whenever the source offers it.
- Silent-install arguments coming from feeds are validated against a strict pattern.
- Programs that were only *detected* (not added by the user) are installed from GitHub only with a SHA-256 published by GitHub or a valid digital signature.
- Microsoft Catalog packages are rejected when their digital signature is broken; downloads and every redirect must stay on the expected hosts.
- Releases come with `SHA256SUMS.txt` and a build-provenance attestation: `gh attestation verify OmniUpdate-Setup.exe --repo CafeConFenix/OmniUpdate`.
- A restore point is created before drivers or system updates are installed.
- `git pull` is always `--ff-only`; local changes are never touched.

## Known limits

- Release files are only as trustworthy as the signing setup: while a release is not Authenticode-signed, Windows SmartScreen will warn. Verify the SHA-256 or the attestation.
- Temporary downloads live in the user's temp folder; malware already running as the same user could race the verify-then-run window. Do not run OmniUpdate on a machine you already consider compromised.

## Reporting

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability**.
