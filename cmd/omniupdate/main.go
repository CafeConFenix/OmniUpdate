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
	if *noBrowser {
		fmt.Println(pageURL)
	} else {
		openWindow(pageURL)
	}
	app.watchdog(*noBrowser)
}

// openWindow opens the interface in its own window (Edge/Chrome app mode).
func openWindow(pageURL string) {
	b := sys.FindBrowser()
	if b == "" {
		_ = sys.OpenURL(pageURL)
		return
	}
	profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "OmniUpdate", "window")
	_ = os.MkdirAll(profile, 0o755)
	c := sys.Command(b, "--app="+pageURL, "--user-data-dir="+profile, "--window-size=1120,800",
		"--no-first-run", "--no-default-browser-check", "--disable-features=Translate,msEdgeSidebar")
	if err := c.Start(); err != nil {
		_ = sys.OpenURL(pageURL)
	}
}

// watchdog closes the program when the window is closed (if no task is running).
func (a *App) watchdog(keepAlive bool) {
	for {
		time.Sleep(2 * time.Second)
		a.mu.Lock()
		idle := a.phase == "idle"
		gone := a.seenPing && time.Since(a.lastPing) > 20*time.Second
		never := !a.seenPing && time.Since(a.started) > 2*time.Minute
		a.mu.Unlock()
		if keepAlive {
			continue
		}
		if idle && (gone || never) {
			return
		}
	}
}
