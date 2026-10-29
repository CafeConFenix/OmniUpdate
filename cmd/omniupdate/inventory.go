package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const inventoryScript = `
$ErrorActionPreference = 'SilentlyContinue'
$os   = Get-CimInstance Win32_OperatingSystem
$cs   = Get-CimInstance Win32_ComputerSystem
$bios = Get-CimInstance Win32_BIOS
$bb   = Get-CimInstance Win32_BaseBoard
$gpus = @(Get-CimInstance Win32_VideoController | ForEach-Object {
  [pscustomobject]@{ Name=$_.Name; Version=$_.DriverVersion; Provider=$_.AdapterCompatibility
    Date=$(if ($_.DriverDate) { $_.DriverDate.ToString('yyyy-MM-dd') } else { '' }) } })
$sd = @{}
Get-CimInstance Win32_PnPSignedDriver | ForEach-Object { if ($_.DeviceID) { $sd[$_.DeviceID.ToUpper()] = $_ } }
$devs = @(Get-CimInstance Win32_PnPEntity | Where-Object { $_.PNPDeviceID } | ForEach-Object {
  $d = $sd[$_.PNPDeviceID.ToUpper()]
  [pscustomobject]@{
    Name=$_.Name; Class=$_.PNPClass; Manufacturer=$_.Manufacturer; DeviceId=$_.PNPDeviceID
    Problem=[int]$_.ConfigManagerErrorCode; HwIds=@($_.HardwareID)
    Provider=$(if ($d) { $d.DriverProviderName } else { '' })
    Version=$(if ($d) { $d.DriverVersion } else { '' })
    Date=$(if ($d -and $d.DriverDate) { $d.DriverDate.ToString('yyyy-MM-dd') } else { '' })
    Inf=$(if ($d) { $d.InfName } else { '' })
    Signed=$(if ($d -and $d.IsSigned -eq $false) { $false } else { $true })
  } })
$paths = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
         'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*',
         'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
$apps = @(Get-ItemProperty $paths | Where-Object { $_.DisplayName -and -not $_.SystemComponent -and -not $_.ParentKeyName } |
  ForEach-Object { [pscustomobject]@{ Name=$_.DisplayName; Version=$_.DisplayVersion; Publisher=$_.Publisher
    Urls=((@($_.URLInfoAbout,$_.HelpLink,$_.URLUpdateInfo,$_.Readme) | Where-Object { $_ }) -join ' ') } } |
  Sort-Object Name -Unique)
[pscustomobject]@{
  Os=[pscustomobject]@{ Caption=$os.Caption; Build=[int]$os.BuildNumber; Arch=$os.OSArchitecture }
  Sys=[pscustomobject]@{ Manufacturer=$cs.Manufacturer; Model=$cs.Model; BiosVendor=$bios.Manufacturer
    BiosVersion=$bios.SMBIOSBIOSVersion; Board=$bb.Product; BoardMaker=$bb.Manufacturer; BiosDate=$(if ($bios.ReleaseDate) { $bios.ReleaseDate.ToString('yyyy-MM-dd') } else { '' }) }
  Gpus=$gpus; Devices=$devs; Apps=$apps
} | ConvertTo-Json -Depth 5 -Compress
`

// strList accepts a string, a list or null in the JSON.
type strList []string

func (s *strList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one != "" {
			*s = strList{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return nil
	}
	for _, m := range many {
		if m != "" {
			*s = append(*s, m)
		}
	}
	return nil
}

type rawDevice struct {
	Name, Class, Manufacturer, DeviceId string
	Problem                             int
	HwIds                               strList
	Provider, Version, Date, Inf        string
	Signed                              bool
}

type invRaw struct {
	Os struct {
		Caption string
		Build   int
		Arch    string
	}
	Sys struct {
		Manufacturer, Model, BiosVendor, BiosVersion, BiosDate, Board, BoardMaker string
	}
	Gpus    []GPU
	Devices []rawDevice
	Apps    []rawApp
}

// rawApp is an installed program as given by the registry (with the web addresses it declares).
type rawApp struct {
	Name, Version, Publisher, Urls string
}

func parseInventory(out string) (*invRaw, error) {
	out = strings.TrimSpace(out)
	i := strings.Index(out, "{")
	if i < 0 {
		return nil, fmt.Errorf("sin datos JSON")
	}
	var inv invRaw
	if err := json.Unmarshal([]byte(out[i:]), &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

func (a *App) scanInventory(ctx context.Context) *invRaw {
	a.stage("sistema", "running", "")
	a.logT("Equipo: leyendo todos los dispositivos y programas instalados…", "System: reading all installed devices and programs…")
	r := runPS(ctx, 10*time.Minute, inventoryScript)
	a.diagf("=== inventario (código %d) ===\n%s", r.Exit, truncate(r.Out, 3000))
	inv, err := parseInventory(r.Out)
	if err != nil {
		a.logT("Equipo: no se pudo leer el inventario (%v).", "System: could not read the inventory (%v).", err)
		a.stage("sistema", "error", T("No se pudo leer el equipo", "Could not read this PC"))
		return nil
	}
	a.mu.Lock()
	a.system = SystemInfo{
		OS: strings.TrimSpace(inv.Os.Caption), Build: inv.Os.Build, Arch: inv.Os.Arch,
		Manufacturer: strings.TrimSpace(inv.Sys.Manufacturer), Model: strings.TrimSpace(inv.Sys.Model),
		BIOSVendor: strings.TrimSpace(inv.Sys.BiosVendor), BIOSVersion: strings.TrimSpace(inv.Sys.BiosVersion),
		BIOSDate: inv.Sys.BiosDate, GPUs: inv.Gpus,
		Board: strings.TrimSpace(inv.Sys.Board), BoardMaker: strings.TrimSpace(inv.Sys.BoardMaker),
	}
	a.programs = make([]Program, 0, len(inv.Apps))
	for _, p := range inv.Apps {
		a.programs = append(a.programs, Program{Name: p.Name, Version: p.Version, Publisher: p.Publisher})
	}
	a.mu.Unlock()
	a.logT("Equipo: %d dispositivos y %d programas instalados.", "System: %d devices and %d installed programs.", len(inv.Devices), len(inv.Apps))
	a.stage("sistema", "done", Tf("%d dispositivos · %d programas", "%d devices · %d programs", len(inv.Devices), len(inv.Apps)))
	return inv
}

// ---------------------------------------------------------------- analysis

var acpiVendorRe = regexp.MustCompile(`^ACPI\\(INT|AMD|NVDA|QCOM|MSHW|ELAN|SYNA|HPQ|DLL|LEN|ASUS|ATK|MSI|ITE)[A-Z0-9]`)

// hwBus returns the bus type of a physical device ("" if virtual/software).
func hwBus(deviceID string) string {
	u := strings.ToUpper(deviceID)
	switch {
	case strings.HasPrefix(u, `PCI\`):
		return "pci"
	case strings.HasPrefix(u, `USB\VID_`) && !strings.Contains(u, "&MI_"):
		return "usb"
	case strings.HasPrefix(u, `HDAUDIO\FUNC_`):
		return "hda"
	case acpiVendorRe.MatchString(u):
		return "acpi"
	}
	return ""
}

var skipClasses = map[string]bool{
	"computer": true, "volume": true, "volumesnapshot": true, "processor": true, "printqueue": true,
	"printer": true, "endpoint": true, "audioendpoint": true, "softwarecomponent": true,
	"softwaredevice": true, "monitor": true, "hidclass": true, "keyboard": true, "mouse": true,
	"diskdrive": true, "cdrom": true, "wpd": true,
}

var alarmClasses = map[string]bool{
	"display": true, "net": true, "media": true, "bluetooth": true, "camera": true,
	"image": true, "scsiadapter": true, "hdc": true,
}

var (
	rePCI  = regexp.MustCompile(`^PCI\\VEN_[0-9A-F]{4}&DEV_[0-9A-F]{4}`)
	reUSB  = regexp.MustCompile(`^USB\\VID_[0-9A-F]{4}&PID_[0-9A-F]{4}`)
	reHDA  = regexp.MustCompile(`^HDAUDIO\\FUNC_[0-9]+&VEN_[0-9A-F]{4}&DEV_[0-9A-F]{4}`)
	reACPI = regexp.MustCompile(`^\*?(ACPI\\)?[A-Z]{3,4}[0-9A-F]{4}$`)
)

// reduceHwID picks the most useful hardware identifier for searching the
// catalog (vendor + model, without subsystem or revision).
func reduceHwID(d rawDevice) string {
	for _, id := range d.HwIds {
		u := strings.ToUpper(strings.TrimSpace(id))
		for _, re := range []*regexp.Regexp{rePCI, reUSB, reHDA} {
			if m := re.FindString(u); m != "" {
				return m
			}
		}
	}
	if hwBus(d.DeviceId) == "acpi" {
		for _, id := range d.HwIds {
			u := strings.ToUpper(strings.TrimSpace(id))
			if reACPI.MatchString(u) {
				return strings.TrimPrefix(u, "*")
			}
		}
	}
	return ""
}

func problemText(code int) string {
	switch code {
	case 0:
		return ""
	case 1:
		return T("Mal configurado", "Not configured correctly")
	case 10:
		return T("El dispositivo no puede iniciarse", "The device cannot start")
	case 14:
		return T("Necesita reiniciar el equipo", "Needs a restart")
	case 18:
		return T("Hay que reinstalar el controlador", "The driver must be reinstalled")
	case 22:
		return T("Desactivado", "Disabled")
	case 24:
		return T("No está presente o no funciona", "Not present or not working")
	case 28:
		return T("Sin controlador instalado", "No driver installed")
	case 31:
		return T("No funciona bien (el controlador no cargó)", "Not working properly (the driver did not load)")
	case 37:
		return T("El controlador no pudo iniciarse", "The driver could not start")
	case 39:
		return T("Controlador dañado o ausente", "Driver damaged or missing")
	case 43:
		return T("Windows detuvo el dispositivo por un error", "Windows stopped the device because of an error")
	case 52:
		return T("Windows no puede verificar la firma del controlador", "Windows cannot verify the driver signature")
	}
	return Tf("Error de dispositivo (código %d)", "Device error (code %d)", code)
}

type lookupTarget struct {
	Key     string
	Devices []rawDevice
	Reasons map[string]bool // problem, generic, old, check
}

func parseISODate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

// analyzeDevices classifies the devices and decides which ones to look up in the catalog.
func analyzeDevices(devs []rawDevice, exhaustive bool, now time.Time) ([]Device, map[string]*lookupTarget) {
	var rows []Device
	targets := map[string]*lookupTarget{}
	for _, d := range devs {
		cls := strings.ToLower(d.Class)
		bus := hwBus(d.DeviceId)
		if skipClasses[cls] {
			continue
		}
		hasProblem := d.Problem != 0 && d.Problem != 22
		if bus == "" && !hasProblem {
			continue
		}
		if bus == "usb" && strings.Contains(strings.ToLower(d.Name), "hub") {
			continue
		}
		name := strings.TrimSpace(d.Name)
		if name == "" {
			name = T("Dispositivo desconocido", "Unknown device")
		}
		row := Device{Name: name, Class: d.Class, Provider: d.Provider, Version: d.Version, Date: d.Date, Status: "ok", devID: d.DeviceId}
		isMS := strings.Contains(strings.ToLower(d.Provider), "microsoft")
		reasons := map[string]bool{}
		switch {
		case hasProblem:
			row.Status, row.Note = "problem", problemText(d.Problem)
			reasons["problem"] = true
		case bus != "" && isMS && alarmClasses[cls]:
			row.Status = "generic"
			if cls == "display" {
				row.Note = T("Controlador básico de Microsoft (sin aceleración gráfica del fabricante)", "Basic Microsoft driver (no manufacturer graphics acceleration)")
			} else {
				row.Note = T("Controlador genérico de Microsoft", "Generic Microsoft driver")
			}
			reasons["generic"] = true
		case bus != "" && !isMS && d.Date != "":
			if t, ok := parseISODate(d.Date); ok && now.Sub(t) > 3*365*24*time.Hour {
				row.Status, row.Note = "old", Tf("Controlador de %d", "Driver from %d", t.Year())
				reasons["old"] = true
			}
		}
		if !d.Signed {
			row.Note = Cat(strings.TrimSpace(row.Note+" · "), T("sin firma digital", "not digitally signed"))
		}
		rows = append(rows, row)
		if bus == "" && !hasProblem {
			continue
		}
		if !exhaustive && len(reasons) == 0 {
			continue
		}
		key := reduceHwID(d)
		if key == "" {
			continue
		}
		t := targets[key]
		if t == nil {
			t = &lookupTarget{Key: key, Reasons: map[string]bool{}}
			targets[key] = t
		}
		t.Devices = append(t.Devices, d)
		for r := range reasons {
			t.Reasons[r] = true
		}
		if len(reasons) == 0 {
			t.Reasons["check"] = true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		rank := map[string]int{"problem": 0, "generic": 1, "old": 2, "update": 3, "ok": 4}
		if rank[rows[i].Status] != rank[rows[j].Status] {
			return rank[rows[i].Status] < rank[rows[j].Status]
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows, targets
}
