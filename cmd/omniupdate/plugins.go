package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plugins and extensions: VS Code (and VSCodium) extensions, global npm packages
// and Chocolatey packages. Everything is checked and updated without windows.

// ------------------------------------------------------------ VS Code

type codeEditor struct {
	Name   string
	Exe    string
	Market string // "ms" (Microsoft Marketplace) | "openvsx"
}

func findCodeEditors() []codeEditor {
	lad := os.Getenv("LOCALAPPDATA")
	list := []codeEditor{
		{"Visual Studio Code", firstExisting(filepath.Join(lad, `Programs\Microsoft VS Code\bin\code.cmd`), filepath.Join(pf(), `Microsoft VS Code\bin\code.cmd`)), "ms"},
		{"VS Code Insiders", firstExisting(filepath.Join(lad, `Programs\Microsoft VS Code Insiders\bin\code-insiders.cmd`), filepath.Join(pf(), `Microsoft VS Code Insiders\bin\code-insiders.cmd`)), "ms"},
		{"VSCodium", firstExisting(filepath.Join(lad, `Programs\VSCodium\bin\codium.cmd`), filepath.Join(pf(), `VSCodium\bin\codium.cmd`)), "openvsx"},
	}
	var out []codeEditor
	for _, e := range list {
		if e.Exe != "" {
			out = append(out, e)
		}
	}
	return out
}

type extRow struct{ ID, Version string }

var extIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*\.[A-Za-z0-9][A-Za-z0-9._-]*$`)

// parseExtensions reads the output of «code --list-extensions --show-versions»
// (one «publisher.name@version» line per extension, sometimes with «@platform» at the end).
func parseExtensions(out string) []extRow {
	var rows []extRow
	seen := map[string]bool{}
	for _, l := range lineSplitRe.Split(out, -1) {
		l = strings.TrimSpace(l)
		parts := strings.Split(l, "@")
		if len(parts) < 2 || !extIDRe.MatchString(parts[0]) || !hasDigit(parts[1]) {
			continue
		}
		id := strings.ToLower(parts[0])
		if seen[id] {
			continue
		}
		seen[id] = true
		rows = append(rows, extRow{ID: id, Version: parts[1]})
	}
	return rows
}

// marketplaceVersion picks the latest stable version from the Marketplace response,
// ignoring pre-releases and those that require a newer VS Code than the installed one.
func marketplaceVersion(body []byte, codeVer string) (string, bool) {
	var resp struct {
		Results []struct {
			Extensions []struct {
				Versions []struct {
					Version    string `json:"version"`
					Properties []struct {
						Key   string `json:"key"`
						Value string `json:"value"`
					} `json:"properties"`
					TargetPlatform string `json:"targetPlatform"`
				} `json:"versions"`
			} `json:"extensions"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp.Results) == 0 || len(resp.Results[0].Extensions) == 0 {
		return "", false
	}
	for _, v := range resp.Results[0].Extensions[0].Versions {
		if v.TargetPlatform != "" && v.TargetPlatform != "universal" && v.TargetPlatform != "win32-x64" {
			continue
		}
		pre, engine := false, ""
		for _, p := range v.Properties {
			switch p.Key {
			case "Microsoft.VisualStudio.Code.PreRelease":
				pre = strings.EqualFold(p.Value, "true")
			case "Microsoft.VisualStudio.Code.Engine":
				engine = p.Value
			}
		}
		if pre || !hasDigit(v.Version) {
			continue
		}
		if codeVer != "" && engine != "" {
			if min := strings.TrimLeft(engine, "^>=~ "); parseVer(min) != nil && cmpVersion(codeVer, min) < 0 {
				continue
			}
		}
		return v.Version, true
	}
	return "", false
}

func marketplaceBody(id string) []byte {
	b, _ := json.Marshal(map[string]any{
		"filters": []any{map[string]any{
			"criteria":   []any{map[string]any{"filterType": 7, "value": id}},
			"pageNumber": 1, "pageSize": 1, "sortBy": 0, "sortOrder": 0}},
		"assetTypes": []string{},
		"flags":      914,
	})
	return b
}

var pluginHTTP = &http.Client{Timeout: 20 * time.Second}

func latestFromMarketplace(ctx context.Context, id, codeVer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery",
		strings.NewReader(string(marketplaceBody(id))))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json;api-version=3.0-preview.1")
	resp, err := pluginHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, 8<<20)
	if err != nil {
		return "", err
	}
	v, ok := marketplaceVersion(body, codeVer)
	if !ok {
		return "", nil
	}
	return v, nil
}

func latestFromOpenVSX(ctx context.Context, id string) (string, error) {
	parts := strings.SplitN(id, ".", 2)
	if len(parts) != 2 {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://open-vsx.org/api/"+parts[0]+"/"+parts[1], nil)
	if err != nil {
		return "", err
	}
	resp, err := pluginHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var v struct {
		Version string `json:"version"`
	}
	body, err := readLimited(resp.Body, 4<<20)
	if err != nil || json.Unmarshal(body, &v) != nil {
		return "", err
	}
	return v.Version, nil
}

func (a *App) scanVSCode(ctx context.Context) int {
	count := 0
	for _, ed := range findCodeEditors() {
		r := runShell(ctx, 90*time.Second, `chcp 65001>nul & "`+ed.Exe+`" --list-extensions --show-versions`)
		a.diagf("=== %s extensiones (código %d) ===\n%s", ed.Name, r.Exit, truncate(r.Out, 6000))
		rows := parseExtensions(r.Out)
		if len(rows) == 0 {
			continue
		}
		codeVer := ""
		if ed.Market == "ms" {
			vr := runShell(ctx, 30*time.Second, `chcp 65001>nul & "`+ed.Exe+`" --version`)
			if f := strings.Fields(vr.Out); len(f) > 0 && parseVer(f[0]) != nil {
				codeVer = f[0]
			}
		}
		type res struct {
			row extRow
			lat string
			err error
		}
		results := make([]res, len(rows))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 6)
		for i, row := range rows {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, row extRow) {
				defer wg.Done()
				defer func() { <-sem }()
				var lat string
				var err error
				if ed.Market == "ms" {
					lat, err = latestFromMarketplace(ctx, row.ID, codeVer)
				} else {
					lat, err = latestFromOpenVSX(ctx, row.ID)
				}
				results[i] = res{row, lat, err}
			}(i, row)
		}
		wg.Wait()
		fails := 0
		for _, x := range results {
			if x.err != nil {
				fails++
				a.diagf("extensión %s: %v", x.row.ID, x.err)
				continue
			}
			if x.lat == "" || parseVer(x.lat) == nil || parseVer(x.row.Version) == nil || cmpVersion(x.lat, x.row.Version) <= 0 {
				continue
			}
			a.addItem(&Item{ID: "plugin:vscode:" + ed.Exe + ":" + x.row.ID, Kind: "plugin", Group: grpPlugins, Selectable: true,
				Name: x.row.ID, Detail: ed.Name + " · " + T("extensión", "extension"), From: x.row.Version, To: x.lat,
				data: map[string]string{"mgr": "vscode", "id": x.row.ID, "exe": ed.Exe, "editor": ed.Name}})
			count++
		}
		if fails > 0 {
			a.logT("Plugins: %s: no se pudo consultar %d extensión(es) (¿sin conexión?).", "Plugins: %s: could not check %d extension(s) (no connection?).", ed.Name, fails)
		}
	}
	return count
}

func (a *App) installVSCodeExt(ctx context.Context, it *Item) (bool, string, bool) {
	id, exe := it.data["id"], it.data["exe"]
	if !extIDRe.MatchString(id) || exe == "" || strings.ContainsAny(exe, `"&|<>^%`) {
		return false, T("Datos de la extensión no válidos", "Invalid extension data"), false
	}
	r := runShell(ctx, 15*time.Minute, `chcp 65001>nul & "`+exe+`" --install-extension `+id+` --force`)
	a.diagf("=== %s --install-extension %s (código %d) ===\n%s", it.data["editor"], id, r.Exit, truncate(r.Out, 3000))
	if r.Timed {
		return false, T("Tardó demasiado y se canceló", "It took too long and was cancelled"), false
	}
	if r.Exit != 0 {
		return false, Cat(Tf("No se pudo actualizar (código %d)", "Could not update (code %d)", r.Exit), ": "+lastLine(r.Out, 140)), false
	}
	// Check the version that remains: the editor may keep the latest one compatible with its version.
	chk := runShell(ctx, 90*time.Second, `chcp 65001>nul & "`+exe+`" --list-extensions --show-versions`)
	for _, row := range parseExtensions(chk.Out) {
		if row.ID == id {
			if row.Version == it.From {
				return true, T("Ya tienes la última versión compatible con tu editor", "You already have the latest version compatible with your editor"), false
			}
			return true, Tf("Actualizada a la versión %s (reinicia el editor para usarla)", "Updated to version %s (restart the editor to use it)", row.Version), false
		}
	}
	return true, T("Actualizada (reinicia el editor para usarla)", "Updated (restart the editor to use it)"), false
}

// ------------------------------------------------------------ npm

var npmNameRe = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)

type npmOutdated struct{ Name, Current, Latest string }

// parseNpmOutdated reads «npm outdated -g --json».
func parseNpmOutdated(out string) []npmOutdated {
	i := strings.Index(out, "{")
	if i < 0 {
		return nil
	}
	var m map[string]struct {
		Current string `json:"current"`
		Latest  string `json:"latest"`
	}
	if json.Unmarshal([]byte(out[i:]), &m) != nil {
		return nil
	}
	var rows []npmOutdated
	for name, v := range m {
		if !npmNameRe.MatchString(name) || v.Latest == "" || v.Current == "" {
			continue
		}
		if parseVer(v.Latest) != nil && parseVer(v.Current) != nil && cmpVersion(v.Latest, v.Current) <= 0 {
			continue
		}
		rows = append(rows, npmOutdated{name, v.Current, v.Latest})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func (a *App) scanNpm(ctx context.Context) int {
	if runShell(ctx, 20*time.Second, "where npm").Exit != 0 {
		return 0
	}
	r := runShell(ctx, 3*time.Minute, "chcp 65001>nul & npm outdated -g --json --no-fund --no-audit")
	a.diagf("=== npm outdated -g (código %d) ===\n%s", r.Exit, truncate(r.Out, 4000))
	rows := parseNpmOutdated(r.Out)
	for _, x := range rows {
		a.addItem(&Item{ID: "plugin:npm:" + x.Name, Kind: "plugin", Group: grpPlugins, Selectable: true,
			Name: x.Name, Detail: T("paquete global de npm", "global npm package"), From: x.Current, To: x.Latest,
			data: map[string]string{"mgr": "npm", "id": x.Name}})
	}
	return len(rows)
}

func (a *App) installNpm(ctx context.Context, it *Item) (bool, string, bool) {
	id := it.data["id"]
	if !npmNameRe.MatchString(id) {
		return false, T("Nombre de paquete no válido", "Invalid package name"), false
	}
	r := runShell(ctx, 20*time.Minute, "chcp 65001>nul & npm install -g "+id+"@latest --no-fund --no-audit")
	a.diagf("=== npm install -g %s (código %d) ===\n%s", id, r.Exit, truncate(r.Out, 3000))
	switch {
	case r.Timed:
		return false, T("Tardó demasiado y se canceló", "It took too long and was cancelled"), false
	case r.Exit == 0:
		return true, T("Actualizado", "Updated"), false
	}
	return false, Cat(Tf("npm terminó con el código %d", "npm finished with code %d", r.Exit), ": "+lastLine(r.Out, 140)), false
}

// ------------------------------------------------------------ Chocolatey

var chocoNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

type chocoRow struct{ Name, Current, Latest string }

// parseChocoOutdated reads «choco outdated -r» («name|current|new|pinned» lines).
func parseChocoOutdated(out string) []chocoRow {
	var rows []chocoRow
	for _, l := range lineSplitRe.Split(out, -1) {
		f := strings.Split(strings.TrimSpace(l), "|")
		if len(f) < 3 || !chocoNameRe.MatchString(f[0]) || !hasDigit(f[1]) || !hasDigit(f[2]) {
			continue
		}
		if len(f) >= 4 && strings.EqualFold(strings.TrimSpace(f[3]), "true") {
			continue // package pinned by the user
		}
		rows = append(rows, chocoRow{f[0], f[1], f[2]})
	}
	return rows
}

func (a *App) scanChoco(ctx context.Context) int {
	if runShell(ctx, 20*time.Second, "where choco").Exit != 0 {
		return 0
	}
	r := runShell(ctx, 6*time.Minute, "chcp 65001>nul & choco outdated -r --no-color")
	a.diagf("=== choco outdated (código %d) ===\n%s", r.Exit, truncate(r.Out, 4000))
	rows := parseChocoOutdated(r.Out)
	for _, x := range rows {
		a.addItem(&Item{ID: "plugin:choco:" + x.Name, Kind: "plugin", Group: grpPlugins, Selectable: true,
			Name: x.Name, Detail: T("paquete de Chocolatey", "Chocolatey package"), From: x.Current, To: x.Latest,
			data: map[string]string{"mgr": "choco", "id": x.Name}})
	}
	return len(rows)
}

func (a *App) installChoco(ctx context.Context, it *Item) (bool, string, bool) {
	id := it.data["id"]
	if !chocoNameRe.MatchString(id) {
		return false, T("Nombre de paquete no válido", "Invalid package name"), false
	}
	r := runShell(ctx, 45*time.Minute, "chcp 65001>nul & choco upgrade "+id+" -y --no-progress --no-color")
	a.diagf("=== choco upgrade %s (código %d) ===\n%s", id, r.Exit, truncate(r.Out, 3000))
	switch {
	case r.Timed:
		return false, T("Tardó demasiado y se canceló", "It took too long and was cancelled"), false
	case r.Exit == 0:
		return true, T("Actualizado", "Updated"), false
	case r.Exit == 1641 || r.Exit == 3010:
		return true, T("Actualizado (hay que reiniciar para terminarlo)", "Updated (restart to finish)"), true
	}
	return false, Cat(Tf("Chocolatey terminó con el código %d", "Chocolatey finished with code %d", r.Exit), ": "+lastLine(r.Out, 140)), false
}

// installPlugin dispatches according to the package manager.
func (a *App) installPlugin(ctx context.Context, it *Item) (bool, string, bool) {
	switch it.data["mgr"] {
	case "vscode":
		return a.installVSCodeExt(ctx, it)
	case "npm":
		return a.installNpm(ctx, it)
	case "choco":
		return a.installChoco(ctx, it)
	}
	return false, T("Gestor de plugins desconocido", "Unknown plugin manager"), false
}
