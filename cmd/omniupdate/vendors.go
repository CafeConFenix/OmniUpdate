package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"omniupdate/internal/sys"
)

// Manufacturer engine. There are two kinds:
//   - Dell, Lenovo and HP: the program installs the manufacturer's official tool by
//     itself and runs it in automatic mode (no windows).
//   - ASUS: queries the ASUS driver catalog and downloads and installs each
//     package with the silent parameters published by the manufacturer itself
//     (see vendor_asus.go).

type oemSpec struct {
	Key, Vendor string
	Tokens      []string // words that identify the manufacturer
	Winget      []string // winget ids of the tool
	FallbackURL string   // official direct download if winget does not have it
	FallbackArg string
	Exe         func() string // path of the command-line executable, "" if not installed
	Run         func(a *App, ctx context.Context, exe string) (ok bool, msg string, reboot bool)
}

func pf() string {
	if v := os.Getenv("ProgramFiles"); v != "" {
		return v
	}
	return `C:\Program Files`
}

func pf86() string {
	if v := os.Getenv("ProgramFiles(x86)"); v != "" {
		return v
	}
	return `C:\Program Files (x86)`
}

func firstExisting(patterns ...string) string {
	for _, p := range patterns {
		if m, _ := filepath.Glob(p); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

var oemSpecs = []oemSpec{
	{Key: "dell", Vendor: "Dell", Tokens: []string{"dell"},
		Winget: []string{"Dell.CommandUpdate", "Dell.CommandUpdate.Universal"},
		Exe: func() string {
			return firstExisting(filepath.Join(pf86(), `Dell\CommandUpdate\dcu-cli.exe`), filepath.Join(pf(), `Dell\CommandUpdate\dcu-cli.exe`))
		},
		Run: runDell},
	{Key: "lenovo", Vendor: "Lenovo", Tokens: []string{"lenovo"},
		Winget:      []string{"Lenovo.SystemUpdate"},
		FallbackURL: "https://download.lenovo.com/pccbbs/thinkvantage_en/system_update_5.08.02.25.exe",
		FallbackArg: "/VERYSILENT /NORESTART",
		Exe: func() string {
			return firstExisting(filepath.Join(pf86(), `Lenovo\System Update\TVSU.exe`), filepath.Join(pf(), `Lenovo\System Update\TVSU.exe`))
		},
		Run: runLenovo},
	{Key: "hp", Vendor: "HP", Tokens: []string{"hp", "hewlett"},
		Winget: []string{"HP.ImageAssistant"},
		Exe: func() string {
			return firstExisting(`C:\SWSetup\HPImageAssistant\HPImageAssistant.exe`, `C:\SWSetup\*\HPImageAssistant.exe`,
				filepath.Join(pf(), `HP\HP Image Assistant\HPImageAssistant.exe`), filepath.Join(pf86(), `HP\HP Image Assistant\HPImageAssistant.exe`))
		},
		Run: runHP},
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func hasWord(s string, tokens []string) bool {
	for _, w := range words(s) {
		for _, t := range tokens {
			if w == t {
				return true
			}
		}
	}
	return false
}

func oemFor(manufacturer string) *oemSpec {
	for i := range oemSpecs {
		if hasWord(manufacturer, oemSpecs[i].Tokens) {
			return &oemSpecs[i]
		}
	}
	return nil
}

func isASUS(s string) bool { return hasWord(s, []string{"asus", "asustek"}) }

func isMSI(s string) bool {
	return hasWord(s, []string{"msi"}) || strings.Contains(strings.ToLower(s), "micro-star")
}

// scanVendors proposes the machine manufacturer's own updates.
func (a *App) scanVendors(ctx context.Context, inv *invRaw) {
	a.stage("fabricante", "running", "")
	a.mu.Lock()
	sysinfo := a.system
	a.mu.Unlock()
	maker := sysinfo.Manufacturer + " " + sysinfo.BoardMaker

	switch {
	case isASUS(maker):
		a.scanASUS(ctx, inv, sysinfo)
		return
	case isMSI(maker):
		// MSI does not publish an automatic tool nor a catalog with hardware identifiers
		// nor silent install parameters: it cannot be installed safely without
		// supervision. Windows Update and the Microsoft Catalog are used instead.
		a.logT("Fabricante: MSI no ofrece instalación automática propia; se usan Windows Update y el Catálogo de Microsoft.", "Manufacturer: MSI offers no automatic installation of its own; Windows Update and the Microsoft Catalog are used.")
		a.stage("fabricante", "done", T("MSI: se usan Windows Update y el Catálogo de Microsoft", "MSI: Windows Update and the Microsoft Catalog are used"))
		return
	}
	spec := oemFor(sysinfo.Manufacturer)
	if spec == nil {
		a.logT("Fabricante: «%s» no tiene herramienta de actualización automática; se usan Windows Update y el Catálogo de Microsoft.", "Manufacturer: “%s” has no automatic update tool; Windows Update and the Microsoft Catalog are used.", sysinfo.Manufacturer)
		a.stage("fabricante", "done", T("Sin herramienta específica para este equipo", "No specific tool for this PC"))
		return
	}
	state := T("se instalará automáticamente", "it will be installed automatically")
	if spec.Exe() != "" {
		state = T("ya instalada", "already installed")
	}
	a.addItem(&Item{ID: "oem:" + spec.Key, Kind: "oem", Group: grpFabr, Selectable: true,
		Name:   Tf("Actualizaciones del fabricante (%s)", "Manufacturer updates (%s)", spec.Vendor),
		Detail: Cat(Tf("Herramienta oficial de %s (", "Official %s tool (", spec.Vendor), state, T("): busca e instala BIOS, firmware y controladores de tu modelo", "): finds and installs the BIOS, firmware and drivers for your model")),
		To:     T("Buscar e instalar", "Find and install"),
		Note:   T("Funciona sola, sin ventanas. Si incluye una BIOS, se aplica al reiniciar: no apagues el equipo durante ese reinicio.", "It runs by itself, with no windows. If it includes a BIOS update, it is applied on restart: do not switch the PC off during that restart."),
		data:   map[string]string{"key": spec.Key}})
	a.stage("fabricante", "done", Tf("%s: 1 herramienta lista", "%s: 1 tool ready", spec.Vendor))
}

func (a *App) installOEM(ctx context.Context, it *Item) (ok bool, msg string, reboot bool) {
	var spec *oemSpec
	for i := range oemSpecs {
		if oemSpecs[i].Key == it.data["key"] {
			spec = &oemSpecs[i]
		}
	}
	if spec == nil {
		return false, T("Fabricante no reconocido", "Manufacturer not recognised"), false
	}
	exe := spec.Exe()
	if exe == "" {
		a.setItem(it, "working", Tf("Instalando la herramienta de %s…", "Installing the %s tool…", spec.Vendor))
		a.setProgress(-1, Tf("Instalando la herramienta oficial de %s…", "Installing the official %s tool…", spec.Vendor))
		for _, id := range spec.Winget {
			if !wingetIDRe.MatchString(id) {
				continue
			}
			r := runShell(ctx, 30*time.Minute, "chcp 65001>nul & winget install --id "+id+" --exact --silent"+
				" --accept-package-agreements --accept-source-agreements --disable-interactivity")
			a.diagf("=== winget install %s (código %d) ===\n%s", id, r.Exit, truncate(r.Out, 5000))
			if exe = spec.Exe(); exe != "" {
				break
			}
		}
		if exe == "" && spec.FallbackURL != "" {
			dest := filepath.Join(tempDir, "dl", filepath.Base(spec.FallbackURL))
			if err := downloadAllowed(ctx, spec.FallbackURL, dest, hostIs("download.lenovo.com")); err != nil {
				a.diagf("descarga %s: %v", spec.FallbackURL, err)
			} else {
				r := runShell(ctx, 30*time.Minute, `"`+dest+`" `+spec.FallbackArg)
				a.diagf("=== instalador %s (código %d) ===\n%s", dest, r.Exit, truncate(r.Out, 3000))
				_ = os.Remove(dest)
				exe = spec.Exe()
			}
		}
		if exe == "" {
			return false, Tf("No se pudo instalar la herramienta de %s (consulta el informe técnico)", "Could not install the %s tool (see the technical report)", spec.Vendor), false
		}
	}
	a.setItem(it, "working", Tf("Buscando e instalando actualizaciones de %s… (puede tardar bastante)", "Finding and installing %s updates… (this can take a long time)", spec.Vendor))
	a.setProgress(-1, Tf("Aplicando las actualizaciones del fabricante (%s)…", "Applying manufacturer updates (%s)…", spec.Vendor))
	return spec.Run(a, ctx, exe)
}

func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return lastLine(decodeOutput(b), n)
}

func runDell(a *App, ctx context.Context, exe string) (bool, string, bool) {
	logf := filepath.Join(tempDir, "dcu.log")
	_ = os.Remove(logf)
	r := runCmd(ctx, 3*time.Hour, sys.Command(exe, "/applyUpdates", "-reboot=disable", "-silent", "-outputlog="+logf))
	a.diagf("=== dcu-cli (código %d) ===\n%s\n%s", r.Exit, truncate(r.Out, 4000), tail(logf, 300))
	switch r.Exit {
	case 0:
		return true, T("Actualizaciones de Dell aplicadas", "Dell updates applied"), false
	case 1:
		return true, T("Actualizaciones de Dell aplicadas; hay que reiniciar para terminarlas", "Dell updates applied; restart to finish them"), true
	case 500:
		return true, T("Dell no tiene nada pendiente para tu equipo", "Dell has nothing pending for your PC"), false
	}
	return false, Tf("Dell Command Update terminó con el código %d. %s", "Dell Command Update finished with code %d. %s", r.Exit, tail(logf, 160)), false
}

func runLenovo(a *App, ctx context.Context, exe string) (bool, string, bool) {
	r := runCmd(ctx, 3*time.Hour, sys.Command(exe, "/CM", "-search", "A", "-action", "INSTALL",
		"-includerebootpackages", "3", "-nolicense", "-exporttowmi", "-noreboot", "-noicon"))
	a.diagf("=== TVSU (código %d) ===\n%s", r.Exit, truncate(r.Out, 4000))
	if r.Exit == 0 {
		return true, T("Actualizaciones de Lenovo aplicadas (si incluyen BIOS o firmware, se completan al reiniciar)", "Lenovo updates applied (if they include BIOS or firmware, they finish on restart)"), true
	}
	return false, Tf("Lenovo System Update terminó con el código %d", "Lenovo System Update finished with code %d", r.Exit), false
}

func runHP(a *App, ctx context.Context, exe string) (bool, string, bool) {
	rep := filepath.Join(tempDir, "hpia", "report")
	dl := filepath.Join(tempDir, "hpia", "dl")
	_ = os.MkdirAll(rep, 0o755)
	_ = os.MkdirAll(dl, 0o755)
	r := runCmd(ctx, 3*time.Hour, sys.Command(exe, "/Operation:Analyze", "/Category:All", "/selection:All", "/action:Install",
		"/silent", "/reportFolder:"+rep, "/softpaqdownloadfolder:"+dl))
	a.diagf("=== HPIA (código %d) ===\n%s", r.Exit, truncate(r.Out, 4000))
	switch r.Exit {
	case 0:
		return true, T("Actualizaciones de HP aplicadas", "HP updates applied"), false
	case 256, 257:
		return true, T("HP no tiene nada pendiente para tu equipo", "HP has nothing pending for your PC"), false
	case 3010:
		return true, T("Actualizaciones de HP aplicadas; hay que reiniciar para terminarlas", "HP updates applied; restart to finish them"), true
	}
	return false, Tf("HP Image Assistant terminó con el código %d", "HP Image Assistant finished with code %d", r.Exit), false
}

// ------------------------------------------------ winget missing

const wingetSetupScript = `
$ErrorActionPreference = 'Stop'
try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  $tmp = $env:TEMP
  try {
    $vc = Join-Path $tmp 'vclibs.appx'
    Invoke-WebRequest -Uri 'https://aka.ms/Microsoft.VCLibs.x64.14.00.Desktop.appx' -OutFile $vc -UseBasicParsing
    Add-AppxPackage -Path $vc -ErrorAction SilentlyContinue
  } catch { }
  $f = Join-Path $tmp 'winget.msixbundle'
  Invoke-WebRequest -Uri 'https://aka.ms/getwinget' -OutFile $f -UseBasicParsing
  Add-AppxPackage -Path $f -ForceApplicationShutdown
  'OK'
} catch { 'ERR: ' + $_.Exception.Message }
`

func (a *App) installWingetSetup(ctx context.Context) (bool, string) {
	r := runPS(ctx, 20*time.Minute, wingetSetupScript)
	out := strings.TrimSpace(r.Out)
	a.diagf("=== instalar winget (código %d) ===\n%s", r.Exit, truncate(out, 3000))
	if strings.HasSuffix(out, "OK") {
		return true, T("Instalado. Pulsa «Buscar de nuevo» para revisar tus programas", "Installed. Press “Scan again” to check your programs")
	}
	return false, Cat(T("No se pudo instalar winget: ", "Could not install winget: "), lastLine(out, 160))
}
