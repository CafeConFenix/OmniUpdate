# Changelog

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
