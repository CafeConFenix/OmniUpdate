package main

import (
	"time"
)

// Sample data (--demo): lets you see the interface without touching the system.
func (a *App) demoScan() {
	a.mu.Lock()
	a.system = SystemInfo{OS: "Microsoft Windows 11 Pro", Build: 26100, Arch: "64 bits", Manufacturer: "LENOVO",
		Model: "ThinkPad T14 Gen 3", BIOSVendor: "LENOVO", BIOSVersion: "N3AET82W (1.52)", BIOSDate: "2023-02-14",
		GPUs: []GPU{{Name: "Intel(R) Iris(R) Xe Graphics", Version: "31.0.101.4502", Date: "2023-03-02", Provider: "Intel Corporation"}}}
	a.programs = []Program{{"7-Zip 23.01 (x64)", "23.01", "Igor Pavlov"}, {"Git", "2.43.0", "The Git Development Community"},
		{"Google Chrome", "118.0.5993.118", "Google LLC"}, {"Microsoft Office 365", "16.0.17328", "Microsoft Corporation"},
		{"Notepad++", "8.5.0", "Notepad++ Team"}, {"Python 3.12.0", "3.12.0", "Python Software Foundation"},
		{"Spotify", "1.2.26", "Spotify AB"}, {"VLC media player", "3.0.18", "VideoLAN"}, {"Zoom", "5.16.2", "Zoom Video Communications"}}
	a.mu.Unlock()
	a.stage("sistema", "running", "")
	time.Sleep(1200 * time.Millisecond)
	a.stage("sistema", "done", Tf("%d dispositivos · %d programas", "%d devices · %d programs", 8, 9))
	a.stage("programas", "running", "")
	a.stage("windows", "running", "")
	a.stage("extras", "running", "")
	time.Sleep(1200 * time.Millisecond)
	a.stage("programas", "done", Tf("%d actualización(es)", "%d update(s)", 7))
	a.stage("extras", "done", Tf("%d elemento(s)", "%d item(s)", 9))
	a.stage("drivers", "running", "")
	for i := 1; i <= 8; i++ {
		a.stageFrac("drivers", float64(i)/8, Tf("%d de %d dispositivos", "%d of %d devices", i, 8))
		time.Sleep(350 * time.Millisecond)
	}
	a.stage("windows", "done", Tf("%d de Windows/Office y %d controlador(es)", "%d for Windows/Office and %d driver(s)", 4, 1))
	a.stage("drivers", "done", Tf("%d controlador(es) más nuevos", "%d newer driver(s)", 3))
	a.stage("fabricante", "running", "")
	time.Sleep(600 * time.Millisecond)
	a.stage("fabricante", "done", T("Lenovo: 1 herramienta lista", "Lenovo: 1 tool ready"))
	add := func(it *Item) {
		it.data = map[string]string{}
		a.addItem(it)
	}
	for _, p := range [][4]string{{"Google Chrome", "Google.Chrome", "118.0.5993.118", "119.0.6045.160"},
		{"7-Zip 23.01 (x64)", "7zip.7zip", "23.01", "24.07"}, {"VLC media player", "VideoLAN.VLC", "3.0.18", "3.0.21"},
		{"Notepad++", "Notepad++.Notepad++", "8.5.0", "8.6.0"}, {"Python 3.12", "Python.Python.3.12", "3.12.0", "3.12.1"},
		{"Git", "Git.Git", "2.43.0", "2.44.0"}, {"Spotify", "Spotify.Spotify", "Unknown", "1.2.50"}} {
		add(&Item{ID: "winget:" + p[1], Kind: "winget", Group: grpProgs, Name: p[0], Detail: p[1] + " · winget", From: p[2], To: p[3], Selectable: true})
	}

	// Missing components
	add(&Item{ID: "missing:vc", Kind: "winget", Group: grpFaltan, Selectable: true,
		Name: T("Visual C++ 2015-2022 Redistributable (32 bits)", "Visual C++ 2015-2022 Redistributable (32-bit)"), Detail: "Microsoft.VCRedist.2015+.x86 · winget",
		From: T("No instalado", "Not installed"), To: T("Instalar", "Install"), Note: T("Programas y juegos de 32 bits lo usan.", "32-bit programs and games use it.")})
	add(&Item{ID: "missing:wv2", Kind: "winget", Group: grpFaltan, Selectable: true,
		Name: "Microsoft Edge WebView2 Runtime", Detail: "Microsoft.EdgeWebView2Runtime · winget",
		From: T("No instalado", "Not installed"), To: T("Instalar", "Install"), Note: T("Lo usan muchas aplicaciones modernas para mostrar contenido web.", "Many modern apps use it to display web content.")})
	add(&Item{ID: "cat:0", Kind: "catalog", Group: grpFaltan, Selectable: true,
		Name: "Intel(R) Smart Sound Technology (Intel(R) SST) Audio Controller", Detail: Tf("%s · %s · Catálogo de Microsoft Update", "%s · %s · Microsoft Update Catalog", "Intel MEDIA Driver Update (10.29.0.9825)", "05/08/2025"),
		From: T("Sin controlador", "No driver"), To: "10.29.0.9825", Note: T("El dispositivo no tiene controlador instalado.", "The device has no driver installed.")})

	// Plugins, GitHub and repositories
	add(&Item{ID: "plugin:vscode:a", Kind: "plugin", Group: grpPlugins, Selectable: true, Name: "ms-python.python",
		Detail: Cat("Visual Studio Code · ", T("extensión", "extension")), From: "2024.14.1", To: "2025.4.0"})
	add(&Item{ID: "plugin:vscode:b", Kind: "plugin", Group: grpPlugins, Selectable: true, Name: "esbenp.prettier-vscode",
		Detail: Cat("Visual Studio Code · ", T("extensión", "extension")), From: "10.1.0", To: "11.0.0"})
	add(&Item{ID: "plugin:npm:t", Kind: "plugin", Group: grpPlugins, Selectable: true, Name: "typescript",
		Detail: T("paquete global de npm", "global npm package"), From: "5.3.3", To: "5.6.3"})
	add(&Item{ID: "github:BurntSushi/ripgrep", Kind: "github", Group: grpGithub, Selectable: true, Name: "ripgrep",
		Detail: "BurntSushi/ripgrep · GitHub · ripgrep-14.1.1-x86_64-pc-windows-msvc.zip", From: "14.1.0", To: "14.1.1",
		Note: Join(" · ", "6 MB", T("Programa portátil: se copia a la carpeta de OmniUpdate (se indica la ruta al terminar).", "Portable program: it is copied to the OmniUpdate folder (the path is shown when it finishes)."))})
	add(&Item{ID: "github:obsproject/obs-studio", Kind: "github", Group: grpGithub, Selectable: true, Name: "OBS Studio",
		Detail: "obsproject/obs-studio · GitHub · OBS-Studio-31.0.0-Windows-Installer.exe", From: "30.2.3", To: "31.0.0",
		Note: Join(" · ", "122 MB", T("Detectado por la web del programa (GitHub); comprueba que es el mismo", "Detected from the program's website (GitHub); check it is the same one"))})
	add(&Item{ID: "repo:C:\\dev\\mi-proyecto", Kind: "repo", Group: grpRepos, Selectable: true, Name: "mi-proyecto",
		Detail: "C:\\dev\\mi-proyecto · main ← origin/main · https://github.com/usuario/mi-proyecto", From: "a1b2c3d", To: "e4f5a6b",
		Note: Tf("%d commit(s) nuevos", "%d new commit(s)", 4)})
	add(&Item{ID: "repo:C:\\dev\\otro", Kind: "repo", Group: grpRepos, Name: "otro",
		Detail: "C:\\dev\\otro · main ← origin/main", From: "9f8e7d6", To: "1c2d3e4",
		Note: Join(" · ", Tf("%d commit(s) nuevos", "%d new commit(s)", 2), Tf("Tu rama tiene %d commit(s) propios sin subir: hay que fusionar a mano", "Your branch has %d local commit(s) that are not pushed: merge manually", 1))})

	add(&Item{ID: "wu:1", Kind: "wu", Group: grpWindows, Name: "Actualización acumulativa 2025-10 para Windows 11 Version 24H2 (KB5044284)", Detail: "Actualizaciones de seguridad", To: "KB5044284", Note: "612 MB", Selectable: true})
	add(&Item{ID: "wu:2", Kind: "wu", Group: grpWindows, Name: "Actualización de seguridad de .NET 8.0.10", Detail: "Actualizaciones de seguridad", To: "KB5045123", Note: "74 MB", Selectable: true})
	add(&Item{ID: "wu:3", Kind: "wu", Group: grpWindows, Name: "Microsoft Office: actualización de seguridad", Detail: "Office 2016 y posteriores", To: "KB5002550", Selectable: true})
	add(&Item{ID: "wu:4", Kind: "wu", Group: grpWindows, Name: "Actualización a Windows 11, versión 25H2", Detail: "Actualizaciones de versión", Note: versionUpgradeNote, Selectable: true})
	add(&Item{ID: "wu:5", Kind: "wu", Group: grpDrivers, Name: "Realtek Semiconductor Corp. - MEDIA - 6.0.9560.1", Detail: "Windows Update · Realtek Realtek High Definition Audio", From: "—", To: "2024-08-21", Selectable: true})
	add(&Item{ID: "cat:1", Kind: "catalog", Group: grpDrivers, Name: "Intel(R) Wi-Fi 6E AX211 160MHz", Detail: Tf("%s · %s · Catálogo de Microsoft Update", "%s · %s · Microsoft Update Catalog", "Intel Net Driver Update (23.60.1.3)", "21/08/2025"), From: "22.250.0.1", To: "23.60.1.3", Selectable: true})
	add(&Item{ID: "cat:2", Kind: "catalog", Group: grpDrivers, Name: "Intel(R) Management Engine Interface, Intel(R) SMBus", Detail: Tf("%s · %s · Catálogo de Microsoft Update", "%s · %s · Microsoft Update Catalog", "Intel System Driver Update (2409.15.0.0)", "03/09/2025"), From: T("Genérico", "Generic"), To: "2409.15.0.0",
		Note: T("Sustituye al controlador genérico de Microsoft por uno del fabricante", "Replaces Microsoft's generic driver with the manufacturer's"), Selectable: true})
	add(&Item{ID: "cat:3", Kind: "catalog", Group: grpDrivers, Name: "Intel(R) Serial IO I2C Host Controller", Detail: Tf("%s · %s · Catálogo de Microsoft Update", "%s · %s · Microsoft Update Catalog", "Intel SoftwareComponent Driver Update (30.100.2437.9)", "11/06/2025"), From: "30.100.2229.7", To: "30.100.2437.9", Selectable: true})
	add(&Item{ID: "oem:lenovo", Kind: "oem", Group: grpFabr, Name: T("Actualizaciones del fabricante (Lenovo)", "Manufacturer updates (Lenovo)"),
		Detail:     T("Herramienta oficial de Lenovo (se instalará automáticamente): busca e instala BIOS, firmware y controladores de tu modelo", "Lenovo's official tool (installed automatically): finds and installs BIOS, firmware and drivers for your model"),
		To:         T("Buscar e instalar", "Find and install"),
		Note:       T("Funciona sola, sin ventanas. Si incluye una BIOS, se aplica al reiniciar: no apagues el equipo durante ese reinicio.", "Runs by itself, without windows. If it includes a BIOS update, it is applied on restart: do not power off the PC during that restart."),
		Selectable: true})
	add(&Item{ID: "problem:x", Kind: "info", Group: grpSinDrv, Name: T("Controlador de comunicaciones simples PCI", "PCI Simple Communications Controller"), Detail: `PCI\VEN_8086&DEV_51E0&SUBSYS_22A317AA`,
		Note: Cat(T("Sin controlador instalado", "No driver installed"), ". ", T("Ninguna fuente (Windows Update, Catálogo de Microsoft y fabricante) ofrece un controlador para este dispositivo.", "No source (Windows Update, Microsoft Catalog or the manufacturer) offers a driver for this device."))})
	a.mu.Lock()
	a.devices = []Device{
		{Name: T("Controlador de comunicaciones simples PCI", "PCI Simple Communications Controller"), Class: "System", Status: "problem", Note: T("Sin controlador instalado", "No driver installed")},
		{Name: "Intel(R) Wi-Fi 6E AX211 160MHz", Class: "Net", Provider: "Intel", Version: "22.250.0.1", Date: "2023-03-01", Status: "update", Note: Tf("Hay una versión más nueva: %s", "A newer version is available: %s", "23.60.1.3")},
		{Name: "Intel(R) Management Engine Interface", Class: "System", Provider: "Microsoft", Version: "10.0.26100.1", Date: "2006-06-21", Status: "generic", Note: T("Controlador genérico de Microsoft", "Generic Microsoft driver")},
		{Name: "Intel(R) Iris(R) Xe Graphics", Class: "Display", Provider: "Intel", Version: "31.0.101.4502", Date: "2023-03-02", Status: "ok"},
		{Name: "Realtek High Definition Audio", Class: "Media", Provider: "Realtek", Version: "6.0.9381.1", Date: "2022-04-04", Status: "old", Note: Tf("Controlador de %d", "Driver from %d", 2022)},
		{Name: "NVMe SSD Controller", Class: "SCSIAdapter", Provider: "Microsoft", Version: "10.0.26100.1", Date: "2006-06-21", Status: "ok"},
		{Name: "USB4 Host Router", Class: "System", Provider: "Microsoft", Version: "10.0.26100.1", Date: "2006-06-21", Status: "ok"},
		{Name: "Intel(R) Wireless Bluetooth(R)", Class: "Bluetooth", Provider: "Intel", Version: "23.40.0.2", Date: "2024-01-10", Status: "ok"},
	}
	a.mu.Unlock()
	a.logT("Demostración: datos de ejemplo cargados (no se ha tocado el sistema).", "Demo: sample data loaded (the system has not been touched).")
	a.finishScan()
}

func (a *App) demoInstall(sel []*Item) {
	for i, it := range sel {
		a.setItem(it, "working", T("Instalando…", "Installing…"))
		a.setProgress(float64(i)/float64(len(sel))*100, Tf("Instalando %s…", "Installing %s…", it.Name))
		time.Sleep(900 * time.Millisecond)
		if it.ID == "winget:Spotify.Spotify" {
			a.setItem(it, "error", T("winget terminó con el código 0x8A150101: la aplicación está en uso", "winget finished with code 0x8A150101: the application is in use"))
			continue
		}
		a.setItem(it, "done", T("Instalado", "Installed"))
	}
	a.mu.Lock()
	a.reboot = true
	a.summary = T("Terminado (demostración). Reinicia el equipo para completar la instalación.", "Finished (demo). Restart the PC to complete the installation.")
	a.mu.Unlock()
}
