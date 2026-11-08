# Changelog

## 1.3.1
- Rebuilt with Go 1.26: fixes 25 standard-library vulnerabilities present in the 1.3.0 binaries.

## 1.3.0
- **Security**: programs that OmniUpdate only *detected* from an installed program's website (not added by you) are now installed only when the file can be proven genuine: the SHA-256 that GitHub publishes for it, or a valid digital signature. Programs you add yourself behave as before.
- **Security**: Microsoft Update Catalog driver packages are checked for a broken digital signature (tampering) before installation, and download redirects must stay on Microsoft hosts over HTTPS.
- **Security**: the local interface also rejects requests from a foreign `Origin`, and sends `X-Content-Type-Options` and `Referrer-Policy` headers.
- Releases are now built and published by GitHub Actions when a `v*` tag is pushed, with `SHA256SUMS.txt` and a build-provenance attestation (`gh attestation verify`). Optional Authenticode signing (`tools/sign.sh`).
- CI: gofmt, vet, tests, `govulncheck` and a Windows test job. Dependabot, PR and issue templates.
- The version now lives in one place (the `VERSION` file).

## 1.2.1
- Fix: the app window could open empty/grey on some PCs. Edge/Chrome was started with a hidden-window flag; it now starts normally, bypasses system proxies for the local address and disables GPU acceleration (a common cause of blank windows).
- If the app window does not answer within 15 s, the interface is opened automatically in the default browser.
- A "Loading…" placeholder is always visible, script errors are shown on screen instead of leaving an empty window, and everything is logged to `%LOCALAPPDATA%\OmniUpdate\omniupdate.log`.

## 1.2.0
- Renamed to **OmniUpdate** (formerly "Actualizador PC"); the installer removes the old version and its shortcuts.
- **Bilingual interface** (Español / English): follows the Windows language, switch with the ES/EN button, instantly and without rescanning.
- The icon is now embedded in `OmniUpdate-Setup.exe` and `OmniUpdate.exe` (multi-size, plus version info and manifest).
- **Plugins**: VS Code / VSCodium extensions, global npm packages and Chocolatey packages.
- **GitHub programs**: detects installed programs whose website is on GitHub and lets you watch any `owner/repo`; installs MSI / installers / portable zips from the latest release.
- **Git repositories**: finds local clones and fast-forwards them with `git pull --ff-only` (never merges, never touches local changes).
- **Missing components**: Visual C++ runtimes, .NET Desktop Runtime and WebView2 are detected and offered; devices without a driver get a driver searched automatically. One button installs everything that is missing.
- Source tree reorganised for GitHub (README, license, CI, docs).

## 1.1.0
- Rewritten in Go with a single-file installer (no PowerShell).
- Robust, language-independent `winget` parsing with a fallback to the official WinGet PowerShell module.
- Start screen and loading screen with a progress bar.
- ASUS driver feed (verified SHA-256 + Authenticode); Dell, Lenovo and HP tools installed automatically.

## 1.0.0
- First version (PowerShell).
