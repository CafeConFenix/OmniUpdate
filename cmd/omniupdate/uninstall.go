package main

import (
	"os"
	"path/filepath"
	"strings"

	"omniupdate/internal/sys"
)

const uninstallKey = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\OmniUpdate`

func shortcutPaths() []string {
	pd := os.Getenv("ProgramData")
	pub := os.Getenv("PUBLIC")
	return []string{
		filepath.Join(pd, `Microsoft\Windows\Start Menu\Programs`, appName+".lnk"),
		filepath.Join(pub, "Desktop", appName+".lnk"),
	}
}

func runUninstall() {
	if sys.MessageBox(appName, pick(T("¿Quieres desinstalar OmniUpdate de este equipo?", "Do you want to uninstall OmniUpdate from this PC?"), sys.UILang()), sys.MBYesNo|sys.MBIconQuestion) != sys.IDYes {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	for _, p := range shortcutPaths() {
		_ = os.Remove(p)
	}
	_ = sys.Command("reg.exe", "delete", uninstallKey, "/f").Run()
	_ = os.RemoveAll(filepath.Join(os.Getenv("LOCALAPPDATA"), "OmniUpdate"))
	_ = os.RemoveAll(tempDir)
	// The install folder is only deleted if it really is ours.
	if strings.EqualFold(filepath.Base(dir), appName) {
		_ = sys.StartDetached(`ping -n 4 127.0.0.1 >nul & rmdir /s /q "` + dir + `"`)
	}
	sys.MessageBox(appName, pick(T("OmniUpdate se ha desinstalado.", "OmniUpdate has been uninstalled."), sys.UILang()), sys.MBIconInfo)
}
