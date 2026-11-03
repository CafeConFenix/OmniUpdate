package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Sample response with the same shape the ASUS support website returns (GetPDDrivers).
const asusFixture = `{"Status":"OK","Message":"","Result":{"Count":6,"Obj":[
 {"Name":"Networking","Count":2,"Files":[
  {"Id":"1","Version":"V3.4.2.1046","Title":"MediaTek WLAN Driver","FileSize":"11.25 MB","ReleaseDate":"2024/06/20",
   "DownloadUrl":{"Global":"https://dlcdnets.asus.com/pub/ASUS/GamingNB/Image/Driver/Networking/38522/WirelessLan_ROG_MediaTek_J_V3.4.2.1046Sub1_38522_1.exe?model=GA402XY","China":""},
   "ExeModule":"WirelessLan_ROG_MediaTek_J_V3.4.2.1046Sub1_38522_1.exe%%/SUPPRESSMSGBOXES /VERYSILENT /NORESTART","Reboot":"0","Sign":"x","sha256":"CF85579D683CC51207942135952B9850E62EF99CA9F6E4EF51DACAF4ED16492C","INFDate":"06-11-2024",
   "HardwareInfoList":[{"hardwareid":"PCI\\VEN_14C3&DEV_0616&SUBSYS_061614C3","date":null,"version":null},{"hardwareid":"PCI\\VEN_14C3&DEV_7922&SUBSYS_792214C3","date":null,"version":null}]},
  {"Id":"2","Version":"V3.3.2.971","Title":"MediaTek WLAN Driver","FileSize":"11 MB","ReleaseDate":"2024/05/03",
   "DownloadUrl":{"Global":"https://dlcdnets.asus.com/x/WirelessLan_old.exe","China":""},
   "ExeModule":"WirelessLan_old.exe%%/SUPPRESSMSGBOXES /VERYSILENT /NORESTART","Reboot":"0","sha256":"AA","INFDate":"02-23-2024",
   "HardwareInfoList":[{"hardwareid":"PCI\\VEN_14C3&DEV_0616&SUBSYS_061614C3","date":null,"version":null}]}]},
 {"Name":"Chipset","Count":1,"Files":[
  {"Id":"3","Version":"V1.2.0.121","Title":"AMD Chipset","FileSize":"40 MB","ReleaseDate":"2023/05/02",
   "DownloadUrl":{"Global":"https://dlcdnets.asus.com/x/AMD_Chipset.zip","China":""},
   "ExeModule":"AMD_Chipset_DriverOnly.exe%%/SUPPRESSMSGBOXES /VERYSILENT /NORESTART","Reboot":1,"sha256":"BB","INFDate":"04-17-2023",
   "HardwareInfoList":[{"hardwareid":"ACPI\\AMD0010"},{"hardwareid":"ACPI\\AMDI0010"}]}]},
 {"Name":"Audio","Count":2,"Files":[
  {"Id":"4","Version":"latest version at the MS store","Title":"Realtek Codec Console Application","DownloadUrl":{"Global":"9p2b8mcsvpln"},"ExeModule":"installUWP.bat","HardwareInfoList":[]},
  {"Id":"5","Version":"V6.0.9499.1","Title":"Realtek Audio","DownloadUrl":{"Global":"https://evil.example.com/a.exe"},"ExeModule":"a.exe%%/VERYSILENT","HardwareInfoList":[{"hardwareid":"HDAUDIO\\FUNC_01&VEN_10EC&DEV_0256&SUBSYS_104318F4"}]}]},
 {"Name":"BIOS & FIRMWARE","Count":1,"Files":[
  {"Id":"6","Version":"V312","Title":"BIOS","DownloadUrl":{"Global":"https://dlcdnets.asus.com/x/bios.exe"},"ExeModule":"bios.exe%%/S","HardwareInfoList":[{"hardwareid":"ACPI\\FOO"}]}]}
]}}`

func TestParseAsusDrivers(t *testing.T) {
	pk, err := parseAsusDrivers([]byte(asusFixture))
	if err != nil {
		t.Fatal(err)
	}
	// Remaining: WLAN x2 and Chipset. Excluded: Store .bat, foreign domain and BIOS/firmware.
	if len(pk) != 3 {
		t.Fatalf("esperaba 3 paquetes instalables, hay %d: %+v", len(pk), pk)
	}
	p := pk[0]
	if p.Version != "3.4.2.1046" || p.Exe != "WirelessLan_ROG_MediaTek_J_V3.4.2.1046Sub1_38522_1.exe" ||
		p.ExeArgs != "/SUPPRESSMSGBOXES /VERYSILENT /NORESTART" || p.Reboot {
		t.Errorf("paquete 0 mal: %+v", p)
	}
	if p.InfDate.Format("2006-01-02") != "2024-06-11" {
		t.Errorf("fecha INF mal: %v", p.InfDate)
	}
	if len(p.HwIDs) != 2 || p.HwIDs[0] != `PCI\VEN_14C3&DEV_0616&SUBSYS_061614C3` {
		t.Errorf("ids de hardware mal: %v", p.HwIDs)
	}
	if !pk[2].Reboot {
		t.Errorf("el chipset pide reinicio (Reboot numérico 1)")
	}
}

func TestParseAsusErrors(t *testing.T) {
	if _, err := parseAsusDrivers([]byte(`{"Status":"FAIL","Message":"FAIL","Result":null}`)); err == nil {
		t.Errorf("una respuesta FAIL debe dar error")
	}
	if _, err := parseAsusDrivers([]byte(`<html>no es json</html>`)); err == nil {
		t.Errorf("HTML no debe interpretarse como JSON")
	}
}

func TestMatchFeedAndNeedsUpdate(t *testing.T) {
	pk, _ := parseAsusDrivers([]byte(asusFixture))
	wifi := rawDevice{Name: "MediaTek Wi-Fi 6E MT7922", DeviceId: `PCI\VEN_14C3&DEV_0616&SUBSYS_061614C3&REV_00\4&1`,
		HwIds:   strList{`PCI\VEN_14C3&DEV_0616&SUBSYS_061614C3&REV_00`, `PCI\VEN_14C3&DEV_0616&SUBSYS_061614C3`, `PCI\VEN_14C3&DEV_0616`},
		Version: "3.3.2.971", Date: "2024-02-23"}
	chip := rawDevice{Name: "AMD GPIO Controller", DeviceId: `ACPI\AMDI0010\0`, HwIds: strList{`ACPI\AMDI0010`}, Version: "2.2.0.130", Date: "2023-06-01"}
	other := rawDevice{Name: "Otra cosa", DeviceId: `PCI\VEN_1&DEV_2\3`, HwIds: strList{`PCI\VEN_1&DEV_2`}}
	hits := matchFeed(pk, []rawDevice{wifi, chip, other})
	if len(hits) != 1 || hits[0].P.Title != "MediaTek WLAN Driver" || hits[0].P.Version != "3.4.2.1046" {
		t.Fatalf("debe proponer solo la WLAN más nueva: %+v", hits)
	}
	// Same driver already installed: nothing is proposed.
	wifi.Version, wifi.Date = "3.4.2.1046", "2024-06-11"
	if hits := matchFeed(pk, []rawDevice{wifi}); len(hits) != 0 {
		t.Errorf("ya está al día, no debe proponer: %+v", hits)
	}
	// No driver installed: it is proposed.
	if hits := matchFeed(pk, []rawDevice{{DeviceId: wifi.DeviceId, HwIds: wifi.HwIds, Problem: 28}}); len(hits) != 1 {
		t.Errorf("sin controlador debe proponerse")
	}
	// Different version label but same or later date: up to date (avoids always offering it).
	p := feedPkg{Version: "1.2.0.121", InfDate: time.Date(2023, 4, 17, 0, 0, 0, 0, time.UTC)}
	if needsUpdate(rawDevice{Version: "5.0.0.1", Date: "2023-04-17"}, p) {
		t.Errorf("misma fecha: no debe proponer")
	}
	if !needsUpdate(rawDevice{Version: "1.0.0.5", Date: "2022-01-01"}, p) {
		t.Errorf("versión y fecha anteriores: debe proponer")
	}
	if needsUpdate(rawDevice{Version: "abc", Date: ""}, p) {
		t.Errorf("sin datos comparables: no debe proponer")
	}
}

func TestAsusModelCandidates(t *testing.T) {
	c := asusModelCandidates("ROG Zephyrus G14 GA402XY_GA402XY", "GA402XY")
	if len(c) == 0 || c[0] != "GA402XY" {
		t.Errorf("portátil: %v", c)
	}
	c = asusModelCandidates("System Product Name", "ROG STRIX B550-F GAMING")
	if len(c) == 0 || c[0] != "ROG-STRIX-B550-F-GAMING" {
		t.Errorf("sobremesa: %v", c)
	}
	c = asusModelCandidates("ASUS TUF Gaming F15 FX507ZC4_FX507ZC4", "FX507ZC4")
	found := false
	for _, x := range c {
		if x == "FX507ZC" {
			found = true
		}
	}
	if !found {
		t.Errorf("falta la variante sin último carácter: %v", c)
	}
}

func TestSilentArgsAndNames(t *testing.T) {
	if a, ok := parseSilentArgs("/SUPPRESSMSGBOXES /VERYSILENT /NORESTART"); !ok || len(a) != 3 {
		t.Errorf("parámetros válidos rechazados")
	}
	for _, bad := range []string{"", "/S && calc", "-s;rm", "/DIR=C:\\x y", "VERYSILENT", "/a|b"} {
		if _, ok := parseSilentArgs(bad); ok {
			t.Errorf("debería rechazar %q", bad)
		}
	}
	if !safeExeRe.MatchString("Setup (1).exe") || safeExeRe.MatchString(`..\evil.exe`) || safeExeRe.MatchString("a.bat") {
		t.Errorf("nombres de ejecutable mal validados")
	}
	if !asusDownloadHost("dlcdnets.asus.com") || asusDownloadHost("asus.com.evil.net") || asusDownloadHost("notasus.com") {
		t.Errorf("lista de dominios de ASUS mal")
	}
}

func TestUnzipSafe(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.zip")
	mk := func(path string, files map[string]string) {
		f, _ := os.Create(path)
		zw := zip.NewWriter(f)
		for n, c := range files {
			w, _ := zw.Create(n)
			w.Write([]byte(c))
		}
		zw.Close()
		f.Close()
	}
	mk(good, map[string]string{"sub/Setup.exe": "x", "a.txt": "y"})
	out := filepath.Join(dir, "out")
	if err := unzipSafe(good, out); err != nil {
		t.Fatal(err)
	}
	if findFile(out, "setup.exe") == "" {
		t.Errorf("no encuentra Setup.exe (sin distinguir mayúsculas)")
	}
	bad := filepath.Join(dir, "bad.zip")
	mk(bad, map[string]string{"../../evil.txt": "x"})
	if err := unzipSafe(bad, filepath.Join(dir, "out2")); err == nil || !strings.Contains(err.Error(), "ruta") {
		t.Errorf("debe rechazar zip-slip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Errorf("se escribió fuera de la carpeta")
	}
}

func TestVendorDetection(t *testing.T) {
	if !isASUS("ASUSTeK COMPUTER INC.") || !isASUS("ASUS") || isASUS("System manufacturer") {
		t.Errorf("isASUS mal")
	}
	if !isMSI("Micro-Star International Co., Ltd.") || !isMSI("MSI") || isMSI("Dell Inc.") {
		t.Errorf("isMSI mal")
	}
}
