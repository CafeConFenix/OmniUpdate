package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"omniupdate/internal/sys"
)

func (a *App) startInstall(ids []string) (bool, string) {
	a.mu.Lock()
	if a.phase != "idle" {
		a.mu.Unlock()
		return false, T("Ya hay una tarea en curso", "A task is already running")
	}
	var sel []*Item
	seen := map[string]bool{}
	for _, id := range ids {
		it := a.findItem(id)
		if it == nil || !it.Selectable || seen[id] || it.Status == "done" {
			continue
		}
		seen[id] = true
		sel = append(sel, it)
	}
	if len(sel) == 0 {
		a.mu.Unlock()
		return false, T("No hay nada seleccionado", "Nothing is selected")
	}
	a.phase, a.percent, a.step, a.summary = "installing", 0, T("Preparando…", "Preparing…"), ""
	for _, it := range sel {
		it.Status, it.Result = "queued", T("En cola", "Queued")
	}
	a.mu.Unlock()
	go a.runInstall(sel)
	return true, ""
}

const restoreScript = `
try {
  Checkpoint-Computer -Description 'OmniUpdate' -RestorePointType MODIFY_SETTINGS -ErrorAction Stop -WarningVariable w -WarningAction SilentlyContinue
  if ($w) { 'WARN: ' + ($w -join ' ') } else { 'OK' }
} catch { 'ERR: ' + $_.Exception.Message }
`

const driverVersionsScript = `
$ErrorActionPreference = 'SilentlyContinue'
$m = @{}
Get-CimInstance Win32_PnPSignedDriver | ForEach-Object { if ($_.DeviceID) { $m[$_.DeviceID] = [string]$_.DriverVersion } }
$m | ConvertTo-Json -Compress
`

func (a *App) runInstall(sel []*Item) {
	defer func() {
		if r := recover(); r != nil {
			a.logT("Error interno: %v", "Internal error: %v", r)
			a.diagf("panic: %v\n%s", r, debug.Stack())
		}
		a.mu.Lock()
		a.phase, a.percent = "idle", 100
		a.mu.Unlock()
	}()
	if a.demo {
		a.demoInstall(sel)
		return
	}
	ctx := context.Background()
	var wg, oem, vend, setup, wu, cat, extra []*Item
	for _, it := range sel {
		switch it.Kind {
		case "winget":
			wg = append(wg, it)
		case "oem":
			oem = append(oem, it)
		case "vendor":
			vend = append(vend, it)
		case "setup":
			setup = append(setup, it)
		case "wu":
			wu = append(wu, it)
		case "catalog":
			cat = append(cat, it)
		case "plugin", "repo", "github":
			extra = append(extra, it)
		default:
			a.setItem(it, "error", T("Este elemento no se puede instalar desde aquí", "This item cannot be installed from here"))
		}
	}
	total := float64(len(sel))
	done := 0.0
	advance := func(n int) { done += float64(n); a.setProgress(done/total*100, "") }
	reboot := false

	if len(wu)+len(cat)+len(vend)+len(oem) > 0 {
		a.setProgress(-1, T("Creando un punto de restauración por seguridad…", "Creating a restore point for safety…"))
		r := runPS(ctx, 5*time.Minute, restoreScript)
		out := strings.TrimSpace(r.Out)
		switch {
		case strings.HasPrefix(out, "OK"):
			a.logT("Punto de restauración creado.", "Restore point created.")
		case strings.HasPrefix(out, "WARN"):
			a.logT("Ya existía un punto de restauración reciente; se usa ese.", "A recent restore point already existed; using it.")
		default:
			a.logT("No se pudo crear un punto de restauración (¿Protección del sistema desactivada?). Continúo igualmente. %s",
				"Could not create a restore point (is System Protection off?). Continuing anyway. %s", lastLine(out, 150))
		}
	}

	for _, it := range wg {
		fresh := it.data["mode"] == "install"
		if fresh {
			a.setItem(it, "working", T("Instalando…", "Installing…"))
			a.setProgress(-1, Tf("Instalando %s…", "Installing %s…", it.Name))
		} else {
			a.setItem(it, "working", T("Actualizando…", "Updating…"))
			a.setProgress(-1, Tf("Actualizando %s…", "Updating %s…", it.Name))
		}
		ok, msg, rb := a.installWinget(ctx, it)
		a.finishItem(it, ok, msg)
		reboot = reboot || rb
		advance(1)
	}
	for _, it := range setup {
		a.setItem(it, "working", T("Instalando…", "Installing…"))
		a.setProgress(-1, T("Instalando winget…", "Installing winget…"))
		ok, msg := a.installWingetSetup(ctx)
		a.finishItem(it, ok, msg)
		advance(1)
	}
	for _, it := range extra {
		a.setItem(it, "working", T("Actualizando…", "Updating…"))
		a.setProgress(-1, Tf("Actualizando %s…", "Updating %s…", it.Name))
		var ok bool
		var msg string
		var rb bool
		switch it.Kind {
		case "plugin":
			ok, msg, rb = a.installPlugin(ctx, it)
		case "repo":
			ok, msg, rb = a.installRepo(ctx, it)
		case "github":
			ok, msg, rb = a.installGithub(ctx, it)
		}
		a.finishItem(it, ok, msg)
		reboot = reboot || rb
		advance(1)
	}
	if len(wu) > 0 {
		a.setProgress(-1, Tf("Windows Update: instalando %d elemento(s)… (no cierres la ventana)", "Windows Update: installing %d item(s)… (do not close the window)", len(wu)))
		reboot = a.installWU(ctx, wu) || reboot
		advance(len(wu))
	}
	if len(cat) > 0 {
		cl := newCatalogClient()
		var okCat []*Item
		for _, it := range cat {
			a.setItem(it, "working", T("Descargando…", "Downloading…"))
			a.setProgress(-1, Tf("Controlador: %s…", "Driver: %s…", it.Name))
			ok, msg, rb := a.installCatalog(ctx, cl, it)
			if ok {
				okCat = append(okCat, it)
				a.setItem(it, "working", msg)
			} else {
				a.finishItem(it, false, msg)
			}
			reboot = reboot || rb
			advance(1)
		}
		if len(okCat) > 0 {
			a.setProgress(-1, T("Comprobando los controladores instalados…", "Checking the installed drivers…"))
			_ = runCmd(ctx, 3*time.Minute, sys.Command("pnputil.exe", "/scan-devices"))
			a.verifyDevices(ctx, okCat)
		}
	}

	if len(vend) > 0 {
		var okV []*Item
		for _, it := range vend {
			a.setItem(it, "working", T("Descargando…", "Downloading…"))
			a.setProgress(-1, Tf("Fabricante: %s…", "Manufacturer: %s…", it.Name))
			ok, msg, rb := a.installVendor(ctx, it)
			if ok {
				okV = append(okV, it)
				a.setItem(it, "working", msg)
			} else {
				a.finishItem(it, false, msg)
			}
			reboot = reboot || rb
			advance(1)
		}
		if len(okV) > 0 {
			a.setProgress(-1, T("Comprobando los controladores instalados…", "Checking the installed drivers…"))
			_ = runCmd(ctx, 3*time.Minute, sys.Command("pnputil.exe", "/scan-devices"))
			a.verifyDevices(ctx, okV)
		}
	}

	for _, it := range oem {
		a.setItem(it, "working", T("Preparando…", "Preparing…"))
		ok, msg, rb := a.installOEM(ctx, it)
		a.finishItem(it, ok, msg)
		reboot = reboot || rb
		advance(1)
	}

	a.mu.Lock()
	good, bad := 0, 0
	for _, it := range sel {
		if it.Status == "done" {
			good++
		} else {
			bad++
		}
	}
	a.reboot = a.reboot || reboot
	sum := Tf("Terminado: %d correcto(s)", "Finished: %d succeeded", good)
	if bad > 0 {
		sum = Cat(sum, Tf(", %d con problemas", ", %d with problems", bad))
	}
	sum = Cat(sum, ".")
	if a.reboot {
		sum = Cat(sum, T(" Reinicia el equipo para completar la instalación.", " Restart the PC to complete the installation."))
	}
	a.summary = sum
	a.mu.Unlock()
	a.logT("%s", "%s", sum)
}

func (a *App) finishItem(it *Item, ok bool, msg string) {
	if ok {
		a.setItem(it, "done", msg)
	} else {
		a.setItem(it, "error", msg)
	}
}

// installCatalog downloads a catalog driver and installs it with pnputil.
func (a *App) installCatalog(ctx context.Context, cl *catalogClient, it *Item) (bool, string, bool) {
	guid := it.data["guid"]
	if !guidOnlyRe.MatchString(guid) {
		return false, T("Identificador no válido", "Invalid identifier"), false
	}
	url, err := cl.downloadURL(ctx, guid)
	if err != nil {
		return false, Cat(T("No se pudo obtener la descarga: ", "Could not get the download: "), errText(err)), false
	}
	cab := filepath.Join(tempDir, "dl", guid+".cab")
	dir := filepath.Join(tempDir, "drv", guid)
	defer os.Remove(cab)
	defer os.RemoveAll(dir)
	if err := cl.download(ctx, url, cab); err != nil {
		return false, Cat(T("Falló la descarga: ", "Download failed: "), errText(err)), false
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, errText(err), false
	}
	a.setItem(it, "working", T("Instalando…", "Installing…"))
	r := runCmd(ctx, 5*time.Minute, sys.Command("expand.exe", "-F:*", cab, dir))
	if r.Exit != 0 {
		a.diagf("expand %s: %s", guid, r.Out)
		return false, T("No se pudo descomprimir el paquete", "Could not unpack the package"), false
	}
	r = runCmd(ctx, 20*time.Minute, sys.Command("pnputil.exe", "/add-driver", filepath.Join(dir, "*.inf"), "/subdirs", "/install"))
	a.diagf("=== pnputil %s (código %d) ===\n%s", guid, r.Exit, truncate(r.Out, 6000))
	switch r.Exit {
	case 0:
		return true, T("Instalado", "Installed"), false
	case 3010:
		return true, T("Instalado (requiere reinicio)", "Installed (restart required)"), true
	}
	msg := Tf("pnputil terminó con el código %d", "pnputil finished with code %d", r.Exit)
	if ll := lastLine(r.Out, 140); ll != "" {
		msg = Cat(msg, ": "+ll)
	}
	return false, msg, false
}

// verifyDevices checks, after installing, whether the driver version really changed.
func (a *App) verifyDevices(ctx context.Context, items []*Item) {
	r := runPS(ctx, 10*time.Minute, driverVersionsScript)
	now := map[string]string{}
	if js := extractJSON(r.Out); js != "" {
		raw := map[string]string{}
		if json.Unmarshal([]byte(js), &raw) == nil {
			for k, v := range raw {
				now[strings.ToUpper(k)] = v
			}
		}
	}
	for _, it := range items {
		changed := false
		for i, id := range it.devs {
			if v, ok := now[strings.ToUpper(id)]; ok && v != "" && v != it.vers[i] {
				changed = true
			}
		}
		switch {
		case len(now) == 0:
			a.setItem(it, "done", T("Instalado", "Installed"))
		case changed:
			a.setItem(it, "done", Tf("Actualizado a la versión %s", "Updated to version %s", it.To))
		case it.Kind == "vendor":
			a.setItem(it, "done", T("Instalado (el equipo ya usa la versión más reciente del fabricante)", "Installed (the PC already uses the manufacturer's latest version)"))
		default:
			a.setItem(it, "done", T("Añadido al sistema; Windows mantiene el controlador actual (ya es el mejor para tu equipo)",
				"Added to the system; Windows keeps the current driver (it is already the best for your PC)"))
		}
	}
}
