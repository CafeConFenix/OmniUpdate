package main

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Missing components: libraries and runtimes that many programs and games need and that
// Windows doesn't ship with (Visual C++, .NET Desktop, WebView2). They are detected from the list of
// installed programs and only offered if winget knows the package (checked beforehand).

type component struct {
	ID      string         // winget identifier
	Name    string         // bilingual name
	Why     string         // what it is for (bilingual)
	Present *regexp.Regexp // installed program name indicating it is already present
	X64Only bool           // only makes sense on 64-bit Windows
}

var missingComponents = []component{
	{ID: "Microsoft.VCRedist.2015+.x64", X64Only: true,
		Name:    T("Visual C++ 2015-2022 Redistributable (64 bits)", "Visual C++ 2015-2022 Redistributable (64-bit)"),
		Why:     T("Lo necesitan muchos programas y juegos para arrancar.", "Many programs and games need it to start."),
		Present: regexp.MustCompile(`(?i)visual c\+\+ .*(2015|2017|2019|2022|v14).*redistributable.*\(x64\)`)},
	{ID: "Microsoft.VCRedist.2015+.x86",
		Name:    T("Visual C++ 2015-2022 Redistributable (32 bits)", "Visual C++ 2015-2022 Redistributable (32-bit)"),
		Why:     T("Programas y juegos de 32 bits lo usan.", "32-bit programs and games use it."),
		Present: regexp.MustCompile(`(?i)visual c\+\+ .*(2015|2017|2019|2022|v14).*redistributable.*\(x86\)`)},
	{ID: "Microsoft.DotNet.DesktopRuntime.8",
		Name:    T(".NET Desktop Runtime 8", ".NET Desktop Runtime 8"),
		Why:     T("Necesario para abrir aplicaciones de escritorio hechas con .NET 8.", "Required to run desktop apps built with .NET 8."),
		Present: regexp.MustCompile(`(?i)(windows desktop runtime|\.net desktop runtime)\D{0,6}8\.`)},
	{ID: "Microsoft.EdgeWebView2Runtime",
		Name:    T("Microsoft Edge WebView2 Runtime", "Microsoft Edge WebView2 Runtime"),
		Why:     T("Lo usan muchas aplicaciones modernas para mostrar contenido web.", "Many modern apps use it to display web content."),
		Present: regexp.MustCompile(`(?i)webview2 runtime`)},
}

// missingFrom returns the components that don't appear among the installed programs.
func missingFrom(apps []rawApp, arch string) []component {
	is64 := strings.Contains(arch, "64")
	var out []component
	for _, c := range missingComponents {
		if c.X64Only && !is64 {
			continue
		}
		found := false
		for _, p := range apps {
			if c.Present.MatchString(p.Name) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, c)
		}
	}
	return out
}

// wingetKnows checks that winget knows the package (so nothing that can't be installed is ever offered).
func wingetKnows(ctx context.Context, id string) bool {
	if !wingetIDRe.MatchString(id) {
		return false
	}
	r := runShell(ctx, 90*time.Second, "chcp 65001>nul & winget show --id "+id+" --exact --accept-source-agreements --disable-interactivity")
	return r.Exit == 0
}

func (a *App) scanMissing(ctx context.Context, inv *invRaw) int {
	cands := missingFrom(inv.Apps, inv.Os.Arch)
	if len(cands) == 0 {
		return 0
	}
	if runShell(ctx, 40*time.Second, "where winget").Exit != 0 {
		return 0 // the winget warning is already given by the programs section
	}
	known := make([]bool, len(cands))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c component) {
			defer wg.Done()
			defer func() { <-sem }()
			known[i] = wingetKnows(ctx, c.ID)
		}(i, c)
	}
	wg.Wait()
	n := 0
	for i, c := range cands {
		if !known[i] {
			a.diagf("componente %s: winget no lo conoce; no se ofrece", c.ID)
			continue
		}
		a.addItem(&Item{ID: "missing:" + c.ID, Kind: "winget", Group: grpFaltan, Selectable: true,
			Name: c.Name, Detail: c.ID + " · winget", From: T("No instalado", "Not installed"), To: T("Instalar", "Install"),
			Note: c.Why, data: map[string]string{"id": c.ID, "source": "winget", "mode": "install"}})
		n++
	}
	return n
}
