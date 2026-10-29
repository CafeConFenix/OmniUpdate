package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Fallback for reading the program list: Microsoft's official module (Microsoft.WinGet.Client)
// returns objects with the full identifier, without depending on the text table format.
const wingetModuleScript = `
$ErrorActionPreference = 'Stop'
try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  if (-not (Get-Module -ListAvailable -Name Microsoft.WinGet.Client)) {
    try { Install-PackageProvider -Name NuGet -MinimumVersion 2.8.5.201 -Force -Scope AllUsers | Out-Null } catch { }
    Install-Module -Name Microsoft.WinGet.Client -Repository PSGallery -Scope AllUsers -Force -AllowClobber -SkipPublisherCheck
  }
  Import-Module Microsoft.WinGet.Client
  $list = @(Get-WinGetPackage | Where-Object { $_.IsUpdateAvailable } | ForEach-Object {
    [pscustomobject]@{ Name = $_.Name; Id = $_.Id; Version = [string]$_.InstalledVersion
      Available = [string]($_.AvailableVersions | Select-Object -First 1); Source = $_.Source }
  })
  [pscustomobject]@{ Ok = $true; Items = $list } | ConvertTo-Json -Depth 4 -Compress
} catch {
  [pscustomobject]@{ Ok = $false; Error = $_.Exception.Message } | ConvertTo-Json -Compress
}
`

type wgModItem struct {
	Name, ID, Version, Available, Source string
}

type wgModResp struct {
	Ok    bool
	Error string
	Items json.RawMessage
}

func parseWingetModule(out string) ([]wingetPkg, error) {
	js := extractJSON(out)
	var resp wgModResp
	if err := json.Unmarshal([]byte(js), &resp); err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, errString(resp.Error)
	}
	raw := strings.TrimSpace(string(resp.Items))
	var items []wgModItem
	switch {
	case raw == "" || raw == "null":
	case strings.HasPrefix(raw, "["):
		if err := json.Unmarshal(resp.Items, &items); err != nil {
			return nil, err
		}
	default:
		var one wgModItem
		if err := json.Unmarshal(resp.Items, &one); err != nil {
			return nil, err
		}
		items = []wgModItem{one}
	}
	var pk []wingetPkg
	for _, it := range items {
		if !wingetIDRe.MatchString(it.ID) || strings.TrimSpace(it.Available) == "" {
			continue
		}
		src := strings.ToLower(strings.TrimSpace(it.Source))
		if !wingetSrcRe.MatchString(src) {
			src = ""
		}
		pk = append(pk, wingetPkg{Name: strings.TrimSpace(it.Name), ID: it.ID, Version: strings.TrimSpace(it.Version),
			Available: strings.TrimSpace(it.Available), Source: src})
	}
	return pk, nil
}

type errString string

func (e errString) Error() string { return string(e) }

func (a *App) wingetViaModule(ctx context.Context) ([]wingetPkg, bool) {
	r := runPS(ctx, 15*time.Minute, wingetModuleScript)
	a.diagf("=== módulo Microsoft.WinGet.Client (código %d) ===\n%s", r.Exit, truncate(r.Out, 6000))
	pk, err := parseWingetModule(r.Out)
	if err != nil {
		a.logT("Programas: el módulo oficial de winget no está disponible (%v).", "Programs: the official winget module is not available (%v).", err)
		return nil, false
	}
	return pk, true
}
