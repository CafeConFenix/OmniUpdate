# Architecture

OmniUpdate is a single Go program with no third-party dependencies.

```
┌───────────────┐   HTTP + token    ┌──────────────────────────────┐
│ Edge/Chrome   │ ───────────────►  │ cmd/omniupdate (127.0.0.1)   │
│ --app window  │ ◄───────────────  │  server.go  state / scan /   │
│ ui/index.html │   JSON snapshot   │  install / reboot / diag     │
└───────────────┘                   └───────────────┬──────────────┘
                                                    │ runScan (parallel)
        ┌──────────┬───────────┬────────────┬───────┴────┬───────────┬────────────┐
     winget.go    wu.go    inventory.go  plugins.go   githubapps.go  repos.go   missing.go
     (programs)  (Windows) (PnP, apps)   (VS Code,    (GitHub        (git)     (runtimes,
                              │          npm, choco)  releases)               no-driver)
                    catalog.go + vendors.go / vendor_asus.go
                    (Microsoft Update Catalog, Dell / Lenovo / HP / ASUS)
```

- **UI**: one embedded HTML file opened in an Edge/Chrome app window. A watchdog closes the app when the window closes. The server only accepts requests with the per-run token and a loopback `Host`.
- **Scan** (`scan.go`): stages with weights drive the progress bar. Branches run in parallel: programs, Windows/Office, inventory → (catalog + manufacturer), and the extras (plugins, GitHub programs, repositories, missing components).
- **Items** (`model.go`): every finding is an `Item` with a `Kind` (`winget`, `wu`, `catalog`, `oem`, `vendor`, `plugin`, `github`, `repo`, …). `install.go` runs the selected items in a safe order and creates a restore point first when drivers or system updates are involved.
- **Bilingual text** (`i18n.go`): every user-visible string is stored as `spanish␟english`; the UI picks the language when rendering, so switching is instant.
- **Installer** (`cmd/omniupdate-setup`): embeds a gzip of the app, installs it, registers the uninstaller and removes legacy "Actualizador PC" files.
- **Resources** (`tools/mkres`): writes the `.syso` with icon, version info and manifest so the Go linker embeds them in the `.exe`.

## Adding a source

1. Write `scanX(ctx) int` that calls `a.addItem(&Item{…})` and returns the number of findings.
2. Register it in `scanExtras` (`scan.go`) and handle its `Kind` in `runInstall` (`install.go`).
3. Add a parser test with a real output sample, and bilingual texts via `T(...)`.
