package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"omniupdate/internal/sys"
)

//go:embed ui/index.html
var indexHTML string

//go:embed ui/icon.png
var iconPNG []byte

const appName = "OmniUpdate"

var version = "1.0.0"

func main() {
	demo := flag.Bool("demo", false, "modo demostración con datos de ejemplo (no toca el sistema)")
	port := flag.Int("port", 0, "puerto local (0 = automático)")
	noBrowser := flag.Bool("no-browser", false, "no abrir la ventana")
	uninstall := flag.Bool("uninstall", false, "desinstalar")
	flag.Parse()

	if runtime.GOOS == "windows" && !*demo && !sys.IsAdmin() {
		if err := sys.RelaunchElevated(os.Args[1:]); err != nil {
			sys.MessageBox(appName, pick(T("Hacen falta permisos de administrador para revisar controladores.", "Administrator permission is needed to check drivers."), sys.UILang())+"\n\n"+err.Error(), sys.MBIconError)
		}
		return
	}
	if *uninstall {
		runUninstall()
		return
	}
	release, ok := sys.SingleInstance(`Local\OmniUpdate-single-instance`)
	if !ok && !*demo {
		sys.MessageBox(appName, pick(T("OmniUpdate ya está abierto.", "OmniUpdate is already open."), sys.UILang()), sys.MBIconInfo)
		return
	}
	defer release()

	if *demo {
		ghFileOver = filepath.Join(os.TempDir(), "omniupdate-demo-github.json")
		_ = os.Remove(ghFileOver)
	}
	app := newApp(*demo)
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	app.token = hex.EncodeToString(tok)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		sys.MessageBox(appName, pick(T("No se pudo iniciar el servidor local: ", "Could not start the local server: "), sys.UILang())+err.Error(), sys.MBIconError)
		os.Exit(1)
	}
	host := ln.Addr().String()
	srv := &http.Server{Handler: app.routes(host), ReadHeaderTimeout: 10 * time.Second}
	pageURL := fmt.Sprintf("http://%s/?t=%s", host, app.token)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()
	app.pageURL = pageURL
	initLaunchLog()
	launchLogf("OmniUpdate %s · %s/%s · listening on %s", version, runtime.GOOS, runtime.GOARCH, host)
	if *noBrowser {
		fmt.Println(pageURL)
	} else {
		app.openWindow(pageURL)
	}
	app.watchdog(*noBrowser)
}

// launchLog is a small text file (%LOCALAPPDATA%\OmniUpdate\omniupdate.log) written at startup, so that
// even if the window stays blank there is something to send when asking for help.
var launchLogPath string

func initLaunchLog() {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "OmniUpdate")
	_ = os.MkdirAll(dir, 0o755)
	launchLogPath = filepath.Join(dir, "omniupdate.log")
	_ = os.WriteFile(launchLogPath, nil, 0o644)
}

func launchLogf(format string, args ...any) {
	if launchLogPath == "" {
		return
	}
	f, err := os.OpenFile(launchLogPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05 ")+format+"\n", args...)
}

// openWindow opens the interface in its own window (Edge/Chrome app mode).
func (a *App) openWindow(pageURL string) {
	b := sys.FindBrowser()
	if b == "" {
		launchLogf("no Edge/Chrome found: opening the default browser")
		_ = sys.OpenURL(pageURL)
		return
	}
	profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "OmniUpdate", "window")
	_ = os.MkdirAll(profile, 0o755)
	// The browser must be started with CommandVisible: a hidden-window start (SW_HIDE / no window)
	// would make its first window invisible or blank. The flags avoid the usual causes of an empty
	// window: proxies for 127.0.0.1, broken GPU drivers and first-run pages.
	c := sys.CommandVisible(b, "--app="+pageURL, "--user-data-dir="+profile, "--window-size=1120,800",
		"--no-first-run", "--no-default-browser-check", "--no-proxy-server", "--disable-gpu",
		"--disable-features=Translate,msEdgeSidebar,msEdgeStartupBoost")
	launchLogf("browser: %s", b)
	if err := c.Start(); err != nil {
		launchLogf("could not start the browser (%v): opening the default browser", err)
		_ = sys.OpenURL(pageURL)
		return
	}
	launchLogf("browser started (pid %d)", c.Process.Pid)
}

// watchdog closes the program when the window is closed (if no task is running).
func (a *App) watchdog(keepAlive bool) {
	for {
		time.Sleep(2 * time.Second)
		a.mu.Lock()
		idle := a.phase == "idle"
		gone := a.seenPing && time.Since(a.lastPing) > 20*time.Second
		never := !a.seenPing && time.Since(a.started) > 2*time.Minute
		stuck := !a.seenPing && !a.fallback && !keepAlive && time.Since(a.started) > 15*time.Second
		if stuck {
			a.fallback = true
		}
		a.mu.Unlock()
		if stuck {
			// The app window never reached the interface (blank window, blocked, crashed…): try the default browser.
			launchLogf("no answer from the app window after 15 s: opening the default browser")
			a.diagf("The app window did not answer; the interface was opened in the default browser.")
			_ = sys.OpenURL(a.pageURL)
		}
		if keepAlive {
			continue
		}
		if idle && (gone || never) {
			return
		}
	}
}
