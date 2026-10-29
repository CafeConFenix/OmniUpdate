package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"omniupdate/internal/sys"
)

// Groups in the order they are displayed. The identifier is stable; the title is in both languages.
const (
	grpAtencion = "att"
	grpFaltan   = "missing"
	grpProgs    = "progs"
	grpPlugins  = "plugins"
	grpGithub   = "github"
	grpRepos    = "repos"
	grpWindows  = "windows"
	grpDrivers  = "drivers"
	grpFabr     = "vendor"
	grpSinDrv   = "nodrv"
)

// GroupDef describes a group in the updates list.
type GroupDef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

var groupDefs = []GroupDef{
	{grpAtencion, T("Requiere atención", "Needs attention")},
	{grpFaltan, T("Componentes que faltan", "Missing components")},
	{grpProgs, T("Programas", "Programs")},
	{grpPlugins, T("Plugins y extensiones", "Plugins and extensions")},
	{grpGithub, T("Programas de GitHub", "GitHub programs")},
	{grpRepos, T("Repositorios Git", "Git repositories")},
	{grpWindows, T("Windows y Microsoft", "Windows and Microsoft")},
	{grpDrivers, T("Controladores", "Drivers")},
	{grpFabr, T("Fabricante (BIOS, firmware y drivers)", "Manufacturer (BIOS, firmware and drivers)")},
	{grpSinDrv, T("Sin controlador disponible", "No driver available")},
}

// Item is a row in the list: something that can be updated, or a notice.
// All visible texts are bilingual (see i18n.go).
type Item struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // winget | wu | catalog | oem | vendor | setup | plugin | repo | github | info
	Group      string `json:"group"`
	Name       string `json:"name"`
	Detail     string `json:"detail"`
	From       string `json:"from"`
	To         string `json:"to"`
	Note       string `json:"note"`
	Selectable bool   `json:"selectable"`
	Status     string `json:"status"` // "" | working | done | error
	Result     string `json:"result"`

	data map[string]string // internal data used for installing
	devs []string          // affected DeviceIDs (drivers)
	vers []string          // versions installed beforehand (drivers)
}

type Device struct {
	Name     string `json:"name"`
	Class    string `json:"class"`
	Provider string `json:"provider"`
	Version  string `json:"version"`
	Date     string `json:"date"`
	Status   string `json:"status"` // ok | problem | generic | old | update
	Note     string `json:"note"`

	devID string
}

type Program struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
}

type GPU struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Date     string `json:"date"`
	Provider string `json:"provider"`
}

type SystemInfo struct {
	OS           string `json:"os"`
	Build        int    `json:"build"`
	Arch         string `json:"arch"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	BIOSVendor   string `json:"biosVendor"`
	BIOSVersion  string `json:"biosVersion"`
	BIOSDate     string `json:"biosDate"`
	Board        string `json:"board"`
	BoardMaker   string `json:"boardMaker"`
	GPUs         []GPU  `json:"gpus"`
}

// Stage is a step of the scan (shown on the loading screen).
type Stage struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	State  string  `json:"state"` // pending | running | done | error
	Detail string  `json:"detail"`
	Frac   float64 `json:"frac"`
	weight float64
}

type Snapshot struct {
	Stages     []Stage    `json:"stages"`
	ElapsedSec int        `json:"elapsedSec"`
	Version    string     `json:"version"`
	Demo       bool       `json:"demo"`
	Phase      string     `json:"phase"` // idle | scanning | installing
	Percent    float64    `json:"percent"`
	Step       string     `json:"step"`
	Scanned    bool       `json:"scanned"`
	Items      []*Item    `json:"items"`
	Devices    []Device   `json:"devices"`
	Programs   []Program  `json:"programs"`
	System     SystemInfo `json:"system"`
	Log        []string   `json:"log"`
	Summary    string     `json:"summary"`
	Reboot     bool       `json:"reboot"`
	Groups     []GroupDef `json:"groups"`
	Lang       string     `json:"lang"` // Windows language ("es" | "en"): the one used by default
}

type App struct {
	mu       sync.Mutex
	demo     bool
	token    string
	phase    string
	percent  float64
	step     string
	scanned  bool
	items    []*Item
	devices  []Device
	programs []Program
	system   SystemInfo
	logs     []string
	diag     strings.Builder
	summary  string
	reboot   bool
	lastPing time.Time
	seenPing bool
	started  time.Time
	stages   []Stage
	scanBeg  time.Time

	vendorDevs map[string]bool // devices already covered by the manufacturer (to avoid duplicates)
}

func defaultStages() []Stage {
	return []Stage{
		{ID: "sistema", Label: T("Leyendo dispositivos y programas instalados", "Reading installed devices and programs"), State: "pending", weight: 10},
		{ID: "programas", Label: T("Buscando actualizaciones de programas", "Looking for program updates"), State: "pending", weight: 12},
		{ID: "extras", Label: T("Plugins, repositorios Git y programas de GitHub", "Plugins, Git repositories and GitHub programs"), State: "pending", weight: 10},
		{ID: "windows", Label: T("Buscando actualizaciones de Windows y Office", "Looking for Windows and Office updates"), State: "pending", weight: 30},
		{ID: "drivers", Label: T("Buscando controladores de todos tus dispositivos", "Looking for drivers for all your devices"), State: "pending", weight: 28},
		{ID: "fabricante", Label: T("Comprobando las actualizaciones del fabricante", "Checking manufacturer updates"), State: "pending", weight: 10},
	}
}

// stage updates a scan step and recalculates the overall percentage.
func (a *App) stage(id, state, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.stages {
		if a.stages[i].ID != id {
			continue
		}
		a.stages[i].State = state
		if detail != "" || state == "done" {
			a.stages[i].Detail = detail
		}
		switch state {
		case "done", "error":
			a.stages[i].Frac = 1
		case "running":
			if a.stages[i].Frac == 0 {
				a.stages[i].Frac = 0.02
			}
		}
	}
	a.recomputeLocked()
}

func (a *App) stageFrac(id string, f float64, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.stages {
		if a.stages[i].ID == id {
			a.stages[i].Frac = f
			if detail != "" {
				a.stages[i].Detail = detail
			}
		}
	}
	a.recomputeLocked()
}

func (a *App) recomputeLocked() {
	if a.phase != "scanning" {
		return
	}
	var pct, total float64
	for _, st := range a.stages {
		total += st.weight
		pct += st.weight * st.Frac
	}
	if total > 0 {
		a.percent = pct / total * 99
	}
}

func (a *App) markVendorDevs(ids []string) {
	a.mu.Lock()
	if a.vendorDevs == nil {
		a.vendorDevs = map[string]bool{}
	}
	for _, id := range ids {
		a.vendorDevs[strings.ToUpper(id)] = true
	}
	a.mu.Unlock()
}

func newApp(demo bool) *App {
	return &App{demo: demo, phase: "idle", started: time.Now(), lastPing: time.Now()}
}

// logT appends a bilingual line to the log (the UI shows the one in the chosen language).
func (a *App) logT(es, en string, args ...any) {
	line := Cat(time.Now().Format("15:04:05")+"  ", Tf(es, en, args...))
	a.mu.Lock()
	a.logs = append(a.logs, line)
	if len(a.logs) > 500 {
		a.logs = a.logs[len(a.logs)-500:]
	}
	a.mu.Unlock()
}

// diagf stores raw technical text (winget output, errors...) for the report.
func (a *App) diagf(format string, args ...any) {
	a.mu.Lock()
	if a.diag.Len() < 400_000 {
		fmt.Fprintf(&a.diag, format, args...)
		a.diag.WriteString("\n")
	}
	a.mu.Unlock()
}

func (a *App) setProgress(pct float64, step string) {
	a.mu.Lock()
	if pct >= 0 {
		a.percent = pct
	}
	if step != "" {
		a.step = step
	}
	a.mu.Unlock()
}

func (a *App) addItem(it *Item) {
	a.mu.Lock()
	a.items = append(a.items, it)
	a.mu.Unlock()
}

func (a *App) snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]*Item, 0, len(a.items))
	for _, it := range a.items {
		cp := *it // copy: so it is not read while another task modifies it
		items = append(items, &cp)
	}
	stages := append([]Stage{}, a.stages...)
	elapsed := 0
	if a.phase == "scanning" && !a.scanBeg.IsZero() {
		elapsed = int(time.Since(a.scanBeg).Seconds())
	}
	return Snapshot{
		Stages: stages, ElapsedSec: elapsed,
		Version: version, Demo: a.demo, Phase: a.phase, Percent: a.percent, Step: a.step,
		Scanned: a.scanned, Items: items, Devices: append([]Device{}, a.devices...),
		Programs: append([]Program{}, a.programs...), System: a.system,
		Log: append([]string{}, a.logs...), Summary: a.summary, Reboot: a.reboot, Groups: groupDefs, Lang: sys.UILang(),
	}
}

func (a *App) findItem(id string) *Item {
	for _, it := range a.items {
		if it.ID == id {
			return it
		}
	}
	return nil
}

func (a *App) setItem(it *Item, status, result string) {
	a.mu.Lock()
	it.Status = status
	it.Result = result
	a.mu.Unlock()
}
