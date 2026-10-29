package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"omniupdate/internal/sys"
)

func (a *App) routes(host string) http.Handler {
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Host != host {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(a.token)) != 1 &&
				subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(a.token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			h(w, r)
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.Host != host || r.URL.Query().Get("t") != a.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline' 'self'; script-src 'unsafe-inline' 'self'; img-src 'self' data:; connect-src 'self'")
		fmt.Fprint(w, strings.ReplaceAll(indexHTML, "__TOKEN__", a.token))
	})
	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(iconPNG)
	})
	mux.HandleFunc("/api/state", guard(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.lastPing, a.seenPing = time.Now(), true
		a.mu.Unlock()
		writeJSON(w, a.snapshot())
	}))
	mux.HandleFunc("/api/scan", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"ok": a.startScan()})
	}))
	mux.HandleFunc("/api/install", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct{ IDs []string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ok, msg := a.startInstall(req.IDs)
		writeJSON(w, map[string]any{"ok": ok, "error": msg})
	}))
	mux.HandleFunc("/api/reboot", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !a.demo {
			_ = sys.Command("shutdown.exe", "/r", "/t", "20", "/c", "OmniUpdate: reinicio para completar las actualizaciones / restart to finish the updates").Start()
		}
		writeJSON(w, map[string]any{"ok": true})
	}))
	// GitHub programs watched by the user
	mux.HandleFunc("/api/github", guard(func(w http.ResponseWriter, r *http.Request) {
		list := loadGH()
		if list == nil {
			list = []ghEntry{}
		}
		writeJSON(w, list)
	}))
	mux.HandleFunc("/api/github/add", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Repo      string
			Installed bool
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var e ghEntry
		var err error
		if a.demo {
			repo, ok := parseGithubRepo(req.Repo)
			if !ok {
				err = terr("Escribe el repositorio como «propietario/nombre» o pega su dirección de github.com", "Type the repository as “owner/name” or paste its github.com address")
			} else {
				e = ghEntry{Repo: repo, Added: time.Now().Format("2006-01-02")}
				ghMu.Lock()
				list := append(loadGHLocked(), e)
				_ = saveGHLocked(list)
				ghMu.Unlock()
			}
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
			defer cancel()
			e, err = ghAdd(ctx, req.Repo, req.Installed)
		}
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": errText(err)})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "entry": e})
	}))
	mux.HandleFunc("/api/github/remove", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct{ Repo string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := ghRemove(req.Repo); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": errText(err)})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	}))
	mux.HandleFunc("/api/diag", guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		lang := "es"
		if r.URL.Query().Get("lang") == "en" {
			lang = "en"
		}
		fmt.Fprint(w, a.report(lang))
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// report generates the technical report the user can copy (in the chosen language).
func (a *App) report(lang string) string {
	s := a.snapshot()
	p := func(x string) string { return pick(x, lang) }
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s · %s/%s · Go %s\n", appName, version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	if lang == "en" {
		fmt.Fprintf(&b, "System: %s (build %d) %s · %s %s\nMotherboard: %s %s\nBIOS: %s %s (%s)\n", s.System.OS, s.System.Build, s.System.Arch,
			s.System.Manufacturer, s.System.Model, s.System.BoardMaker, s.System.Board, s.System.BIOSVendor, s.System.BIOSVersion, s.System.BIOSDate)
	} else {
		fmt.Fprintf(&b, "Sistema: %s (build %d) %s · %s %s\nPlaca base: %s %s\nBIOS: %s %s (%s)\n", s.System.OS, s.System.Build, s.System.Arch,
			s.System.Manufacturer, s.System.Model, s.System.BoardMaker, s.System.Board, s.System.BIOSVendor, s.System.BIOSVersion, s.System.BIOSDate)
	}
	for _, g := range s.System.GPUs {
		fmt.Fprintf(&b, "GPU: %s %s (%s)\n", g.Name, g.Version, g.Date)
	}
	if lang == "en" {
		fmt.Fprintf(&b, "\nSummary: %s\n\nItems:\n", p(s.Summary))
	} else {
		fmt.Fprintf(&b, "\nResumen: %s\n\nElementos:\n", p(s.Summary))
	}
	for _, it := range s.Items {
		fmt.Fprintf(&b, " - [%s/%s] %s | %s | %s -> %s | %s %s\n", it.Group, it.Kind, p(it.Name), p(it.Detail), p(it.From), p(it.To), it.Status, p(it.Result))
	}
	if lang == "en" {
		b.WriteString("\nLog:\n")
	} else {
		b.WriteString("\nRegistro:\n")
	}
	for _, l := range s.Log {
		b.WriteString(" " + p(l) + "\n")
	}
	a.mu.Lock()
	if lang == "en" {
		b.WriteString("\nTechnical data:\n")
	} else {
		b.WriteString("\nDatos técnicos:\n")
	}
	b.WriteString(a.diag.String())
	a.mu.Unlock()
	return b.String()
}

func exitSoon(code int) {
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(code)
	}()
}
