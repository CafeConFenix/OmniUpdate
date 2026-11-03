package main

import (
	"context"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

func (a *App) safe(name string, wg *sync.WaitGroup, f func()) {
	defer wg.Done()
	defer func() {
		if r := recover(); r != nil {
			a.logT("Error interno en «%s»: %v", "Internal error in “%s”: %v", name, r)
			a.diagf("panic en %s: %v\n%s", name, r, debug.Stack())
		}
	}()
	f()
}

func (a *App) startScan() bool {
	a.mu.Lock()
	if a.phase != "idle" {
		a.mu.Unlock()
		return false
	}
	a.phase = "scanning"
	a.percent, a.step = 0, T("Empezando…", "Starting…")
	a.items, a.devices, a.programs = nil, nil, nil
	a.logs, a.summary = nil, ""
	a.stages = defaultStages()
	a.scanBeg = time.Now()
	a.vendorDevs = nil
	a.diag.Reset()
	a.mu.Unlock()
	go a.runScan()
	return true
}

func (a *App) runScan() {
	defer func() {
		if r := recover(); r != nil {
			a.logT("Error interno: %v", "Internal error: %v", r)
			a.diagf("panic: %v\n%s", r, debug.Stack())
		}
		a.mu.Lock()
		a.phase, a.scanned, a.percent = "idle", true, 100
		a.mu.Unlock()
	}()
	if a.demo {
		a.demoScan()
		return
	}
	ctx := context.Background()

	var wuHw []string
	var hits []catHit
	var targets map[string]*lookupTarget
	invCh := make(chan *invRaw, 1) // the inventory is also needed by the extras and the missing components

	// Four parallel branches: programs, Windows Update, machine (→ catalog + manufacturer) and extras.
	var wg sync.WaitGroup
	wg.Add(4)
	go a.safe("winget", &wg, func() { a.scanWinget(ctx) })
	go a.safe("windows update", &wg, func() { wuHw = a.scanWU(ctx) })
	go a.safe("equipo", &wg, func() {
		var inv *invRaw
		defer func() { invCh <- inv }()
		inv = a.scanInventory(ctx)
		build := 0
		if inv != nil {
			var rows []Device
			rows, targets = analyzeDevices(inv.Devices, true, time.Now())
			a.mu.Lock()
			a.devices = rows
			build = a.system.Build
			a.mu.Unlock()
		} else {
			a.stage("drivers", "error", T("No se pudieron leer los dispositivos", "Could not read the devices"))
		}
		var wg2 sync.WaitGroup
		wg2.Add(2)
		go a.safe("catálogo", &wg2, func() {
			if inv != nil {
				hits = a.collectCatalog(ctx, targets, build)
			}
		})
		go a.safe("fabricante", &wg2, func() { a.scanVendors(ctx, inv) })
		wg2.Wait()
	})
	go a.safe("extras", &wg, func() { a.scanExtras(ctx, invCh) })
	wg.Wait()

	a.applyCatalogHits(hits, wuHw)
	a.addProblemItems(targets)
	a.dedupeGithub()
	a.finishScan()
}

// scanExtras gathers plugins, Git repositories, GitHub programs and missing components.
func (a *App) scanExtras(ctx context.Context, invCh <-chan *invRaw) {
	a.stage("extras", "running", "")
	var wg sync.WaitGroup
	counts := make([]int, 5)
	run := func(i int, name string, f func() int) {
		wg.Add(1)
		go a.safe(name, &wg, func() { counts[i] = f() })
	}
	run(0, "vscode", func() int { return a.scanVSCode(ctx) })
	run(1, "npm", func() int { return a.scanNpm(ctx) })
	run(2, "choco", func() int { return a.scanChoco(ctx) })
	run(3, "repos", func() int { return a.scanRepos(ctx) })
	inv := <-invCh
	run(4, "github+faltan", func() int {
		n := 0
		if inv != nil {
			n += a.scanGithub(ctx, inv.Apps)
			n += a.scanMissing(ctx, inv)
		}
		return n
	})
	wg.Wait()
	total := 0
	for _, c := range counts {
		total += c
	}
	a.logT("Extras: %d elemento(s) (plugins %d, repositorios %d, GitHub y componentes %d).",
		"Extras: %d item(s) (plugins %d, repositories %d, GitHub and components %d).", total, counts[0]+counts[1]+counts[2], counts[3], counts[4])
	a.stage("extras", "done", Tf("%d elemento(s)", "%d item(s)", total))
}

// addProblemItems lists the devices with problems for which no source has a driver.
func (a *App) addProblemItems(targets map[string]*lookupTarget) {
	a.mu.Lock()
	var rows []Device
	covered := map[string]bool{}
	for k := range a.vendorDevs {
		covered[k] = true
	}
	for _, d := range a.devices {
		if d.Status == "problem" && !covered[strings.ToUpper(d.devID)] {
			rows = append(rows, d)
		}
	}
	offered := map[string]bool{}
	for _, it := range a.items {
		for _, id := range it.devs {
			offered[strings.ToUpper(id)] = true
		}
	}
	a.mu.Unlock()
	for _, d := range rows {
		if offered[strings.ToUpper(d.devID)] {
			continue
		}
		a.addItem(&Item{ID: "problem:" + d.devID, Kind: "info", Group: grpSinDrv, Name: d.Name,
			Detail: d.devID,
			Note: Cat(d.Note, ". ", T("Ninguna fuente (Windows Update, Catálogo de Microsoft y fabricante) ofrece un controlador para este dispositivo.",
				"No source (Windows Update, Microsoft Catalog or the manufacturer) offers a driver for this device."))})
	}
}

func (a *App) finishScan() {
	a.mu.Lock()
	for i := range a.stages {
		if a.stages[i].State == "pending" || a.stages[i].State == "running" {
			a.stages[i].State, a.stages[i].Frac = "done", 1
		}
	}
	sel, info := 0, 0
	for _, it := range a.items {
		if it.Selectable {
			sel++
		} else {
			info++
		}
	}
	sort.SliceStable(a.items, func(i, j int) bool {
		gi, gj := groupIndex(a.items[i].Group), groupIndex(a.items[j].Group)
		if gi != gj {
			return gi < gj
		}
		if a.items[i].Selectable != a.items[j].Selectable {
			return a.items[i].Selectable
		}
		return strings.ToLower(plainES(a.items[i].Name)) < strings.ToLower(plainES(a.items[j].Name))
	})
	switch {
	case sel == 0 && info == 0:
		a.summary = T("Todo está al día.", "Everything is up to date.")
	case sel == 0:
		a.summary = Tf("Nada que actualizar automáticamente; %d aviso(s) para revisar.", "Nothing to update automatically; %d notice(s) to review.", info)
	default:
		a.summary = Tf("%d actualización(es) disponible(s)", "%d update(s) available", sel)
		if info > 0 {
			a.summary = Cat(a.summary, Tf(" y %d aviso(s)", " and %d notice(s)", info))
		}
		a.summary = Cat(a.summary, ".")
	}
	s := a.summary
	a.mu.Unlock()
	a.logT("Revisión terminada: %s", "Scan finished: %s", s)
}

func groupIndex(g string) int {
	for i, x := range groupDefs {
		if x.ID == g {
			return i
		}
	}
	return len(groupDefs)
}
