// OmniUpdate installer: a single file that copies the program, creates the
// shortcuts and registers it so it can be uninstalled.
package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"omniupdate/internal/sys"
)

//go:embed payload/OmniUpdate.exe.gz
var appGz []byte

//go:embed payload/app.ico
var appIco []byte

const (
	appName      = "OmniUpdate"
	exeName      = "OmniUpdate.exe"
	uninstallKey = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\OmniUpdate`
)

var version = "1.0.0"

// tx picks the text according to the Windows language (Spanish or English).
func tx(es, en string) string {
	if sys.UILang() == "es" {
		return es
	}
	return en
}

func main() {
	silent := flag.Bool("silent", false, "instalar sin preguntar")
	dirFlag := flag.String("dir", "", "carpeta de instalación (por defecto Archivos de programa)")
	flag.Parse()

	if runtime.GOOS == "windows" && !sys.IsAdmin() {
		if err := sys.RelaunchElevated(os.Args[1:]); err != nil {
			sys.MessageBox(appName, tx("Hacen falta permisos de administrador para instalar.", "Administrator permission is needed to install.")+"\n\n"+err.Error(), sys.MBIconError)
		}
		return
	}
	dir := *dirFlag
	if dir == "" {
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		dir = filepath.Join(pf, appName)
	}
	if !*silent {
		msg := fmt.Sprintf(tx("Se va a instalar %s %s en:\n%s\n\nSe crearán accesos directos en el escritorio y en el menú Inicio.\n\n¿Continuar?",
			"%s %s will be installed in:\n%s\n\nShortcuts will be created on the desktop and in the Start menu.\n\nContinue?"), appName, version, dir)
		if sys.MessageBox(appName+tx(" – Instalación", " – Setup"), msg, sys.MBYesNo|sys.MBIconQuestion) != sys.IDYes {
			return
		}
	}
	warnings, err := install(dir)
	if err != nil {
		sys.MessageBox(appName, tx("No se pudo instalar:", "Could not install:")+"\n\n"+err.Error(), sys.MBIconError)
		os.Exit(1)
	}
	if *silent {
		return
	}
	text := appName + tx(" se instaló correctamente.\n\n¿Quieres abrirlo ahora?", " was installed successfully.\n\nDo you want to open it now?")
	flags := uint32(sys.MBYesNo | sys.MBIconInfo)
	if len(warnings) > 0 {
		text = appName + tx(" se instaló, pero hubo avisos:", " was installed, but there were warnings:") + "\n\n- " + strings.Join(warnings, "\n- ") + tx("\n\n¿Quieres abrirlo ahora?", "\n\nDo you want to open it now?")
		flags = sys.MBYesNo | sys.MBIconWarning
	}
	if sys.MessageBox(appName, text, flags) == sys.IDYes {
		_ = sys.Command(filepath.Join(dir, exeName)).Start()
	}
}

func shortcutPaths() []string {
	pd, pub := os.Getenv("ProgramData"), os.Getenv("PUBLIC")
	return []string{
		filepath.Join(pd, `Microsoft\Windows\Start Menu\Programs`, appName+".lnk"),
		filepath.Join(pub, "Desktop", appName+".lnk"),
	}
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func install(dir string) (warnings []string, err error) {
	// Close a previous copy if it is running.
	if runtime.GOOS == "windows" {
		_ = sys.Command("taskkill.exe", "/f", "/im", exeName).Run()
		time.Sleep(1200 * time.Millisecond)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf(tx("no se pudo crear la carpeta %s: %w", "could not create the folder %s: %w"), dir, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(appGz))
	if err != nil {
		return nil, err
	}
	exe, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	exePath := filepath.Join(dir, exeName)
	if err := os.WriteFile(exePath, exe, 0o755); err != nil {
		return nil, fmt.Errorf(tx("no se pudo copiar el programa (¿está abierto?): %w", "could not copy the program (is it running?): %w"), err)
	}
	icoPath := filepath.Join(dir, "OmniUpdate.ico")
	if err := os.WriteFile(icoPath, appIco, 0o644); err != nil {
		warnings = append(warnings, tx("no se pudo copiar el icono", "could not copy the icon"))
	}
	if runtime.GOOS != "windows" {
		return warnings, nil
	}

	// Remove older versions (they were called "Actualizador PC") so there are not two programs or two shortcuts.
	const legacy = `$ErrorActionPreference = 'SilentlyContinue'
$s = New-Object -ComObject WScript.Shell
$dirs = @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'), [Environment]::GetFolderPath('CommonDesktopDirectory'), [Environment]::GetFolderPath('CommonPrograms'))
foreach ($p in $dirs) {
  $f = Join-Path $p 'Actualizador PC.lnk'
  if (Test-Path -LiteralPath $f) { Remove-Item -LiteralPath $f -Force }
}
Remove-Item -LiteralPath (Join-Path $env:LOCALAPPDATA 'ActualizadorPC') -Recurse -Force
Remove-Item -LiteralPath (Join-Path $env:ProgramFiles 'Actualizador PC') -Recurse -Force
Remove-Item -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\ActualizadorPC' -Recurse -Force`
	_ = sys.Command("taskkill.exe", "/f", "/im", "ActualizadorPC.exe").Run()
	_ = sys.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", legacy).Run()

	// Shortcuts (desktop and Start menu, for all users).
	var sb strings.Builder
	sb.WriteString("$s = New-Object -ComObject WScript.Shell\n")
	for _, p := range shortcutPaths() {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		fmt.Fprintf(&sb, "$l = $s.CreateShortcut(%s); $l.TargetPath = %s; $l.WorkingDirectory = %s; $l.IconLocation = %s; $l.Description = %s; $l.Save()\n",
			psQuote(p), psQuote(exePath), psQuote(dir), psQuote(icoPath), psQuote(tx("Actualiza programas, Windows y controladores", "Updates programs, Windows and drivers")))
	}
	if out, err := sys.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", sb.String()).CombinedOutput(); err != nil {
		warnings = append(warnings, tx("no se pudieron crear los accesos directos: ", "could not create the shortcuts: ")+strings.TrimSpace(string(out)))
	}

	// Registry entry for "Installed apps" (uninstallation).
	reg := func(name, typ, val string) error {
		return sys.Command("reg.exe", "add", uninstallKey, "/v", name, "/t", typ, "/d", val, "/f").Run()
	}
	var regErr error
	for _, kv := range [][3]string{
		{"DisplayName", "REG_SZ", appName}, {"DisplayVersion", "REG_SZ", version}, {"Publisher", "REG_SZ", appName},
		{"InstallLocation", "REG_SZ", dir}, {"DisplayIcon", "REG_SZ", icoPath},
		{"UninstallString", "REG_SZ", `"` + exePath + `" --uninstall`},
		{"NoModify", "REG_DWORD", "1"}, {"NoRepair", "REG_DWORD", "1"},
	} {
		if e := reg(kv[0], kv[1], kv[2]); e != nil {
			regErr = e
		}
	}
	if regErr != nil {
		warnings = append(warnings, tx("no se pudo registrar la desinstalación en Windows", "could not register the uninstaller in Windows"))
	}
	return warnings, nil
}
