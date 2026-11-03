# Security

OmniUpdate runs as administrator and installs software, so it is conservative by design:

- The local interface listens only on `127.0.0.1`, with a random per-run token and a Host-header check.
- Downloads are accepted only over HTTPS from the expected hosts (Microsoft, the vendor's own domain, `github.com`) and are verified (SHA-256 / Authenticode) whenever the source offers it.
- Silent-install arguments coming from feeds are validated against a strict pattern.
- A restore point is created before drivers or system updates are installed.
- `git pull` is always `--ff-only`; local changes are never touched.

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability**.
