package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	wingetIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]*$`)
	wingetSrcRe = regexp.MustCompile(`^[a-z][a-z0-9\-]*$`)
	wingetSepRe = regexp.MustCompile(`^-{8,}\s*$`)
	lineSplitRe = regexp.MustCompile(`\r\n|\r|\n`)
	hex8Re      = regexp.MustCompile(`0x[0-9A-Fa-f]{8}`)
)

type wingetPkg struct {
	Name, ID, Version, Available, Source string
	Truncated                            bool // the ID comes truncated with "…" and cannot be used
}

func hasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// cleanWingetLines splits the output into lines and discards the "noise" that winget
// writes when its output does not go to a console (spinner animation, progress bars).
func cleanWingetLines(out string) []string {
	var lines []string
	for _, l := range lineSplitRe.Split(out, -1) {
		l = strings.TrimRight(l, " \t\x00")
		lines = append(lines, l)
	}
	return lines
}

// parseWingetUpgrade reads the "winget upgrade" table without depending on the language or
// on the column positions: each row is interpreted from right to left
// (source, available version, current version, id) and whatever is left is the name.
func parseWingetUpgrade(out string) []wingetPkg {
	var pk []wingetPkg
	inTable := false
	for _, line := range cleanWingetLines(out) {
		t := strings.TrimSpace(line)
		if wingetSepRe.MatchString(t) {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if t == "" {
			inTable = false
			continue
		}
		if p, ok := parseWingetRow(t); ok {
			pk = append(pk, p)
		}
	}
	return pk
}

func parseWingetRow(t string) (wingetPkg, bool) {
	tok := strings.Fields(t)
	n := len(tok)
	if n < 4 {
		return wingetPkg{}, false
	}
	i := n - 1
	var p wingetPkg
	if !hasDigit(tok[i]) && wingetSrcRe.MatchString(tok[i]) {
		p.Source = tok[i]
		i--
	}
	if i < 3 {
		return wingetPkg{}, false
	}
	p.Available = tok[i]
	if !hasDigit(p.Available) {
		return wingetPkg{}, false
	}
	i--
	p.Version = tok[i]
	i--
	if i >= 0 && (tok[i] == "<" || tok[i] == ">" || tok[i] == "≤" || tok[i] == "≥") {
		p.Version = tok[i] + " " + p.Version
		i--
	}
	if i < 1 {
		return wingetPkg{}, false
	}
	p.ID = tok[i]
	p.Name = strings.Join(tok[:i], " ")
	if strings.ContainsAny(p.ID, "…") || strings.HasSuffix(p.ID, "...") {
		p.Truncated = true
		return p, true
	}
	if !wingetIDRe.MatchString(p.ID) {
		return wingetPkg{}, false
	}
	return p, true
}

var colRe = regexp.MustCompile(`\S+(?: \S+)*`)

var summaryLineRe = regexp.MustCompile(`^(\d+)\s+\p{L}[^\d]*$`)

// wingetSummaryCount adds up the numbers in the summary lines that winget writes after each table
// («5 actualizaciones disponibles», «5 upgrades available»…). Used to check that every row was read.
func wingetSummaryCount(out string) int {
	inTable, total := false, 0
	for _, line := range cleanWingetLines(out) {
		t := strings.TrimSpace(line)
		if wingetSepRe.MatchString(t) {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if m := summaryLineRe.FindStringSubmatch(t); m != nil && len(strings.Fields(t)) <= 5 {
			if v, err := strconv.Atoi(m[1]); err == nil {
				total += v
			}
		}
	}
	return total
}

// parseWingetUpgradeByColumns is the fallback method: it uses the positions of
// the header columns.
func parseWingetUpgradeByColumns(out string) []wingetPkg {
	lines := cleanWingetLines(out)
	sep := -1
	for i, l := range lines {
		if wingetSepRe.MatchString(strings.TrimSpace(l)) {
			sep = i
			break
		}
	}
	if sep < 1 {
		return nil
	}
	var cols []int
	for _, m := range colRe.FindAllStringIndex(lines[sep-1], -1) {
		cols = append(cols, m[0])
	}
	if len(cols) < 4 {
		return nil
	}
	cut := func(l string, a int, b int) string {
		if a >= len(l) {
			return ""
		}
		if b < 0 || b > len(l) {
			b = len(l)
		}
		return strings.TrimSpace(l[a:b])
	}
	var pk []wingetPkg
	for _, l := range lines[sep+1:] {
		if strings.TrimSpace(l) == "" {
			break
		}
		end := -1
		if len(cols) > 4 {
			end = cols[4]
		}
		p := wingetPkg{
			Name: cut(l, 0, cols[1]), ID: cut(l, cols[1], cols[2]),
			Version: cut(l, cols[2], cols[3]), Available: cut(l, cols[3], end),
		}
		if len(cols) > 4 {
			p.Source = cut(l, cols[4], -1)
		}
		if !wingetIDRe.MatchString(p.ID) || !hasDigit(p.Available) {
			continue
		}
		pk = append(pk, p)
	}
	return pk
}

func (a *App) scanWinget(ctx context.Context) {
	a.stage("programas", "running", "")
	a.logT("Programas: consultando winget…", "Programs: querying winget…")
	chk := runShell(ctx, 40*time.Second, "where winget")
	if chk.Exit != 0 {
		a.logT("Programas: winget no está disponible en esta cuenta.", "Programs: winget is not available for this account.")
		a.diagf("=== where winget (código %d) ===\n%s", chk.Exit, chk.Out)
		a.stage("programas", "done", T("winget no está instalado", "winget is not installed"))
		a.addItem(&Item{ID: "setup:winget", Kind: "setup", Group: grpProgs, Selectable: true,
			Name:   T("Activar el administrador de programas de Windows (winget)", "Enable the Windows package manager (winget)"),
			Detail: T("Hace falta para comprobar y actualizar tus programas", "Needed to check and update your programs"),
			To:     T("Instalar", "Install"),
			Note:   T("El programa lo descarga e instala solo; después pulsa «Buscar de nuevo».", "The program downloads and installs it by itself; afterwards press “Scan again”.")})
		return
	}
	r := runShell(ctx, 8*time.Minute, "chcp 65001>nul & winget upgrade --include-unknown --accept-source-agreements --disable-interactivity")
	a.diagf("=== winget upgrade (código %d) ===\n%s", r.Exit, truncate(r.Out, 40000))
	pk := parseWingetUpgrade(r.Out)
	if len(pk) == 0 {
		if alt := parseWingetUpgradeByColumns(r.Out); len(alt) > 0 {
			a.logT("Programas: se usó el método de lectura alternativo.", "Programs: the alternative reading method was used.")
			pk = alt
		}
	}
	expected := wingetSummaryCount(r.Out)
	truncated := 0
	for _, p := range pk {
		if p.Truncated {
			truncated++
		}
	}
	if truncated > 0 || expected > len(pk) {
		a.logT("Programas: la lista de winget no se leyó completa (%d de %d); uso el módulo oficial de winget…",
			"Programs: the winget list was not read completely (%d of %d); using the official winget module…", len(pk), expected)
		if mod, ok := a.wingetViaModule(ctx); ok && len(mod) > 0 {
			pk = mod
			truncated = 0
		}
	}
	seen := map[string]bool{}
	for _, p := range pk {
		if p.Truncated {
			continue
		}
		key := p.ID + "@" + p.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		note := ""
		if strings.HasPrefix(p.Version, "<") {
			note = T("Versión instalada aproximada", "Approximate installed version")
		}
		if !hasDigit(p.Version) {
			note = T("No se pudo leer la versión instalada", "Could not read the installed version")
		}
		a.addItem(&Item{ID: "winget:" + key, Kind: "winget", Group: grpProgs, Name: p.Name,
			Detail: p.ID + srcSuffix(p.Source), From: p.Version, To: p.Available, Note: note,
			Selectable: true, data: map[string]string{"id": p.ID, "source": p.Source}})
	}
	readable := len(seen)
	if truncated > 0 || expected > readable {
		// Something could not be read individually: offer to update everything else at once.
		a.addItem(&Item{ID: "winget:all", Kind: "winget", Group: grpProgs, Selectable: true,
			Name:   T("Actualizar el resto de programas que winget detecta", "Update the rest of the programs winget detects"),
			Detail: Tf("winget indica %d actualización(es) y solo se pudieron listar %d", "winget reports %d update(s) and only %d could be listed", maxInt(expected, readable+truncated), readable),
			To:     T("Actualizar todo", "Update all"), data: map[string]string{"all": "1"}})
	}
	if readable == 0 && truncated == 0 && expected == 0 && hex8Re.MatchString(r.Out) {
		a.addItem(&Item{ID: "warn:wingetread", Kind: "info", Group: grpAtencion,
			Name: T("winget devolvió un error al buscar programas", "winget returned an error while looking for programs"), Detail: lastLine(r.Out, 160),
			Note: T("Se reintentará al pulsar «Buscar de nuevo».", "It will be retried when you press “Scan again”.")})
	}
	a.logT("Programas: %d actualización(es) encontradas.", "Programs: %d update(s) found.", readable)
	a.stage("programas", "done", Tf("%d actualización(es)", "%d update(s)", readable))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func srcSuffix(s string) string {
	if s == "" {
		return ""
	}
	return " · " + s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[…recortado…]"
}

// winget codes worth explaining.
const (
	wingetNoUpgrade        = 0x8A15002B
	wingetAlreadyInstalled = 0x8A15010D
)

func (a *App) installWinget(ctx context.Context, it *Item) (bool, string, bool) {
	id, src := it.data["id"], it.data["source"]
	all := it.data["all"] == "1"
	fresh := it.data["mode"] == "install" // missing component: install instead of upgrade
	if !all && !wingetIDRe.MatchString(id) {
		return false, T("Identificador no válido", "Invalid identifier"), false
	}
	line := "chcp 65001>nul & winget upgrade --id " + id + " --exact --silent --include-unknown" +
		" --accept-package-agreements --accept-source-agreements --disable-interactivity"
	if fresh {
		line = "chcp 65001>nul & winget install --id " + id + " --exact --silent" +
			" --accept-package-agreements --accept-source-agreements --disable-interactivity"
	}
	if all {
		line = "chcp 65001>nul & winget upgrade --all --silent --include-unknown" +
			" --accept-package-agreements --accept-source-agreements --disable-interactivity"
		id = "--all"
	} else if wingetSrcRe.MatchString(src) {
		line += " --source " + src
	}
	r := runShell(ctx, 45*time.Minute, line)
	a.diagf("=== winget upgrade %s (código %d) ===\n%s", id, r.Exit, truncate(r.Out, 8000))
	low := strings.ToLower(r.Out)
	reboot := strings.Contains(low, "reboot") || strings.Contains(low, "restart") || strings.Contains(low, "reinici")
	switch {
	case r.Timed:
		return false, T("Tardó demasiado y se canceló", "It took too long and was cancelled"), false
	case r.Exit == 0 && fresh:
		return true, T("Instalado", "Installed"), reboot
	case r.Exit == 0:
		return true, T("Actualizado", "Updated"), reboot
	case uint32(r.Exit) == wingetNoUpgrade:
		return true, T("Ya estaba en la última versión", "Already on the latest version"), false
	case uint32(r.Exit) == wingetAlreadyInstalled:
		return true, T("Ya estaba instalado", "Already installed"), false
	default:
		msg := Tf("winget terminó con el código 0x%08X", "winget finished with code 0x%08X", uint32(r.Exit))
		if ll := lastLine(r.Out, 140); ll != "" {
			msg = Cat(msg, ": "+ll)
		}
		return false, msg, false
	}
}
