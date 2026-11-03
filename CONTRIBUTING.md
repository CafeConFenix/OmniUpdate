# Contributing

Thanks for helping! The app is plain Go (no cgo, no third-party modules) plus one embedded HTML file.

```bash
make test      # unit tests, run on any OS
make demo      # UI with sample data: no changes to your system
make build     # needs Go 1.24+; produces dist/OmniUpdate-Setup.exe (cross-compiled, works from Linux/macOS/Windows)
```

Rules of thumb:

- **Every user-visible string is bilingual** (Spanish / English). In Go use `T("castellano", "english")` or `Tf(...)`; in the UI add the key to both languages of the `I18N` table in `cmd/omniupdate/ui/index.html`.
- **Never download from an unverified host.** New sources must go through `downloadAllowed` / `trustedDownloadURL` and check hashes or signatures when the source provides them.
- **Parsers need tests.** Anything that reads command output (winget, npm, choco, git…) gets a unit test with a real output sample.
- Code, comments, commit messages and docs are written in English.
- Run `gofmt` and `go vet ./...` before opening a pull request.
