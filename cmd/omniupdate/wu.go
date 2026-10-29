package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const muServiceID = "7971f918-a847-4430-9279-4a52d1efe18d" // Microsoft Update (Windows + Office + others)

const wuSearchScript = `
$ErrorActionPreference = 'Stop'
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $session.ClientApplicationID = 'OmniUpdate'
  $searcher = $session.CreateUpdateSearcher()
  $mu = $false
  try {
    $mgr = New-Object -ComObject Microsoft.Update.ServiceManager
    [void]$mgr.AddService2('` + muServiceID + `', 7, '')
    $searcher.ServerSelection = 3
    $searcher.ServiceID = '` + muServiceID + `'
    $mu = $true
  } catch { }
  $res = $searcher.Search('IsInstalled=0 and IsHidden=0')
  $list = @()
  foreach ($u in $res.Updates) {
    $cats = @(); $catIds = @(); $kb = @()
    foreach ($c in $u.Categories) { $cats += $c.Name; $catIds += $c.CategoryID }
    foreach ($k in $u.KBArticleIDs) { $kb += $k }
    $vd = ''
    if ($u.Type -eq 2) { try { $vd = $u.DriverVerDate.ToString('yyyy-MM-dd') } catch { } }
    $list += [pscustomobject]@{
      Id=$u.Identity.UpdateID; Title=$u.Title; Type=[int]$u.Type; Kb=($kb -join ',')
      SizeMB=[math]::Round($u.MaxDownloadSize / 1MB, 1); Cats=($cats -join ', '); CatIds=($catIds -join ',')
      HwId=$u.DriverHardwareID; Model=$u.DriverModel; Manufacturer=$u.DriverManufacturer; VerDate=$vd
      Input=[bool]$u.InstallationBehavior.CanRequestUserInput; Severity=$u.MsrcSeverity
    }
  }
  [pscustomobject]@{ Ok=$true; MU=$mu; Updates=$list } | ConvertTo-Json -Depth 5 -Compress
} catch {
  [pscustomobject]@{ Ok=$false; Error=$_.Exception.Message } | ConvertTo-Json -Compress
}
`

const wuInstallScript = `
$ErrorActionPreference = 'Stop'
$ids = @(%IDS%)
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $session.ClientApplicationID = 'OmniUpdate'
  $searcher = $session.CreateUpdateSearcher()
  try { $searcher.ServerSelection = 3; $searcher.ServiceID = '` + muServiceID + `' } catch { }
  $res = $searcher.Search('IsInstalled=0 and IsHidden=0')
  $col = New-Object -ComObject Microsoft.Update.UpdateColl
  foreach ($u in $res.Updates) {
    if ($ids -contains $u.Identity.UpdateID) { if (-not $u.EulaAccepted) { $u.AcceptEula() }; [void]$col.Add($u) }
  }
  if ($col.Count -eq 0) { [pscustomobject]@{ Ok=$true; Reboot=$false; Results=@() } | ConvertTo-Json -Compress; exit 0 }
  $dl = $session.CreateUpdateDownloader(); $dl.Updates = $col; [void]$dl.Download()
  $inst = $session.CreateUpdateInstaller(); $inst.Updates = $col
  try { $inst.AllowSourcePrompts = $false; $inst.ForceQuiet = $true } catch { }
  $r = $inst.Install()
  $list = @()
  for ($i = 0; $i -lt $col.Count; $i++) {
    $ur = $r.GetUpdateResult($i)
    $list += [pscustomobject]@{ Id=$col.Item($i).Identity.UpdateID; Code=[int]$ur.ResultCode; HResult=[int]$ur.HResult }
  }
  [pscustomobject]@{ Ok=$true; Reboot=[bool]$r.RebootRequired; Results=$list } | ConvertTo-Json -Depth 4 -Compress
} catch {
  [pscustomobject]@{ Ok=$false; Error=$_.Exception.Message } | ConvertTo-Json -Compress
}
`

type wuUpdate struct {
	ID, Title                               string
	Type                                    int
	Kb                                      string
	SizeMB                                  float64
	Cats, CatIds, HwId, Model, Manufacturer string
	VerDate, Severity                       string
	Input                                   bool
}

type wuSearchResult struct {
	Ok      bool
	MU      bool
	Error   string
	Updates []wuUpdate
}

func extractJSON(out string) string {
	out = strings.TrimSpace(out)
	if i := strings.Index(out, "{"); i >= 0 {
		return out[i:]
	}
	return ""
}

const upgradesCategory = "3689bdc8-b205-4af4-8d4a-a63924c5e9d5"

// versionUpgradeNote is the notice that accompanies Windows version upgrades.
var versionUpgradeNote = T("Actualización de versión de Windows: tarda bastante y reinicia el equipo varias veces",
	"Windows version upgrade: it takes a long time and restarts the PC several times")

func (a *App) scanWU(ctx context.Context) (driverHwIDs []string) {
	a.stage("windows", "running", "")
	a.logT("Windows: buscando actualizaciones de Windows, Office y controladores (Microsoft Update)…", "Windows: looking for Windows, Office and driver updates (Microsoft Update)…")
	r := runPS(ctx, 20*time.Minute, wuSearchScript)
	a.diagf("=== Windows Update (código %d) ===\n%s", r.Exit, truncate(r.Out, 6000))
	js := extractJSON(r.Out)
	var res wuSearchResult
	if js == "" || json.Unmarshal([]byte(js), &res) != nil {
		a.logT("Windows: no se pudo consultar Windows Update (%s).", "Windows: could not query Windows Update (%s).", shortErr(r))
		a.stage("windows", "error", T("No se pudo consultar Windows Update", "Could not query Windows Update"))
		a.addItem(&Item{ID: "warn:wu", Kind: "info", Group: grpAtencion, Name: T("No se pudo consultar Windows Update", "Could not query Windows Update"),
			Detail: lastLine(r.Out, 160), Note: T("Se reintentará al pulsar «Buscar de nuevo» (necesita conexión a Internet).", "It will be retried when you press “Scan again” (needs an Internet connection).")})
		return nil
	}
	if !res.Ok {
		a.logT("Windows: Windows Update devolvió un error: %s", "Windows: Windows Update returned an error: %s", res.Error)
		a.stage("windows", "error", T("Windows Update devolvió un error", "Windows Update returned an error"))
		a.addItem(&Item{ID: "warn:wu", Kind: "info", Group: grpAtencion, Name: T("Windows Update devolvió un error", "Windows Update returned an error"),
			Detail: res.Error, Note: T("Se reintentará al pulsar «Buscar de nuevo».", "It will be retried when you press “Scan again”.")})
		return nil
	}
	nSoft, nDrv := 0, 0
	for _, u := range res.Updates {
		if !guidOnlyRe.MatchString(u.ID) {
			continue
		}
		it := &Item{ID: "wu:" + u.ID, Kind: "wu", Selectable: true, Name: strings.TrimSpace(u.Title),
			data: map[string]string{"id": u.ID}}
		if u.SizeMB > 0 {
			it.Note = fmt.Sprintf("%.0f MB", u.SizeMB)
		}
		if u.Type == 2 {
			it.Group = grpDrivers
			it.Detail = strings.TrimSpace("Windows Update · " + strings.TrimSpace(u.Manufacturer+" "+u.Model))
			it.To = u.VerDate
			it.From = "—"
			if u.HwId != "" {
				driverHwIDs = append(driverHwIDs, u.HwId)
			}
			nDrv++
		} else {
			it.Group = grpWindows
			it.Detail = u.Cats
			if u.Kb != "" {
				it.To = "KB" + strings.ReplaceAll(u.Kb, ",", ", KB")
			}
			nSoft++
			if strings.Contains(strings.ToLower(u.CatIds), upgradesCategory) {
				it.Note = Cat(strings.TrimSpace(it.Note+" · "), versionUpgradeNote)
			}
		}
		a.addItem(it)
	}
	if res.MU {
		a.logT("Windows: %d actualización(es) de Windows/Microsoft y %d controlador(es) de Windows Update.", "Windows: %d Windows/Microsoft update(s) and %d driver(s) from Windows Update.", nSoft, nDrv)
	} else {
		a.logT("Windows: (sin Microsoft Update) %d actualización(es) y %d controlador(es).", "Windows: (without Microsoft Update) %d update(s) and %d driver(s).", nSoft, nDrv)
	}
	a.stage("windows", "done", Tf("%d de Windows/Office y %d controlador(es)", "%d for Windows/Office and %d driver(s)", nSoft, nDrv))
	return driverHwIDs
}

var guidOnlyRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type wuInstallResult struct {
	Ok      bool
	Error   string
	Reboot  bool
	Results []struct {
		ID      string
		Code    int
		HResult int
	}
}

// installWU installs the selected Windows Update updates in bulk.
func (a *App) installWU(ctx context.Context, items []*Item) (reboot bool) {
	var quoted []string
	byID := map[string]*Item{}
	for _, it := range items {
		id := it.data["id"]
		if !guidOnlyRe.MatchString(id) {
			a.setItem(it, "error", T("Identificador no válido", "Invalid identifier"))
			continue
		}
		byID[strings.ToLower(id)] = it
		quoted = append(quoted, "'"+id+"'")
		a.setItem(it, "working", T("Descargando e instalando…", "Downloading and installing…"))
	}
	if len(quoted) == 0 {
		return false
	}
	a.logT("Windows Update: descargando e instalando %d actualización(es) (puede tardar)…", "Windows Update: downloading and installing %d update(s) (this can take a while)…", len(quoted))
	script := strings.Replace(wuInstallScript, "%IDS%", strings.Join(quoted, ","), 1)
	r := runPS(ctx, 3*time.Hour, script)
	a.diagf("=== Windows Update instalar (código %d) ===\n%s", r.Exit, truncate(r.Out, 6000))
	var res wuInstallResult
	js := extractJSON(r.Out)
	if js == "" || json.Unmarshal([]byte(js), &res) != nil || !res.Ok {
		msg := res.Error
		if msg == "" {
			msg = Tf("No se pudo completar la instalación (%s)", "The installation could not be completed (%s)", shortErr(r))
		}
		for _, it := range byID {
			a.setItem(it, "error", msg)
		}
		return false
	}
	seen := map[string]bool{}
	for _, x := range res.Results {
		it := byID[strings.ToLower(x.ID)]
		if it == nil {
			continue
		}
		seen[strings.ToLower(x.ID)] = true
		switch x.Code {
		case 2:
			a.setItem(it, "done", T("Instalado", "Installed"))
		case 3:
			a.setItem(it, "done", T("Instalado (con avisos)", "Installed (with warnings)"))
		default:
			a.setItem(it, "error", Tf("Windows Update no pudo instalarlo (código %d, 0x%08X)", "Windows Update could not install it (code %d, 0x%08X)", x.Code, uint32(x.HResult)))
		}
	}
	for id, it := range byID {
		if !seen[id] {
			a.setItem(it, "error", T("Ya no figura como disponible", "It is no longer listed as available"))
		}
	}
	return res.Reboot
}
