package main

import (
	"testing"
	"time"
)

const catalogFixture = `<html><body><form>
<input type="hidden" name="__VIEWSTATE" id="__VIEWSTATE" value="abc123==" />
<input type="hidden" name="__EVENTVALIDATION" value="ev&amp;1" />
<table id="ctl00_catalogBody_updateMatches">
<tr id="headerRow"><th>Title</th><th>Products</th><th>Classification</th><th>Last Updated</th><th>Version</th><th>Size</th><th>Download</th></tr>
<tr id="11111111-2222-3333-4444-555555555555_R0">
 <td class="resultsbottomBorder resultsIconWidth"><a id="11111111-2222-3333-4444-555555555555_link" href="javascript:void(0);" onclick='goToDetails("11111111-2222-3333-4444-555555555555");'>
   Intel Net Driver Update (2.1.5.10)  </a></td>
 <td id="x_C2_R0">Windows 11 Client, version 24H2 and later (Servicing and Upgrade &amp; Servicing Drivers)</td>
 <td>Drivers (Networking)</td><td><span>12/28/2025</span></td><td>2.1.5.10</td><td><span>377 KB</span><span style="display:none">386103</span></td>
 <td><input class="flatBlueButtonDownload" id="11111111-2222-3333-4444-555555555555" type="button" value="Download"/></td></tr>
<tr id="aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee_R1">
 <td><a id="aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee_link">Intel Net Driver Update (2.1.5.7)</a></td>
 <td>Windows 10 and later drivers</td><td>Drivers (Networking)</td><td>9/2/2025</td><td>2.1.5.7</td><td>379 KB</td><td></td></tr>
<tr id="bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee_R2">
 <td><a>Intel Firmware Update (9.9.9.9)</a></td>
 <td>Windows 10 and later drivers</td><td>Drivers (Firmware)</td><td>9/3/2025</td><td>9.9.9.9</td><td>1 KB</td><td></td></tr>
</table>
<a id="ctl00_catalogBody_nextPageLinkText" href="javascript:__doPostBack('ctl00$catalogBody$nextPageLinkText','')">Next &gt;</a>
</form></body></html>`

func TestParseCatalogHTML(t *testing.T) {
	rows, hidden, next := parseCatalogHTML(catalogFixture)
	if len(rows) != 3 {
		t.Fatalf("filas: %d (%+v)", len(rows), rows)
	}
	r := rows[0]
	if r.GUID != "11111111-2222-3333-4444-555555555555" || r.Title != "Intel Net Driver Update (2.1.5.10)" ||
		r.Version != "2.1.5.10" || r.Classification != "Drivers (Networking)" {
		t.Errorf("fila 0 mal: %+v", r)
	}
	if r.Products != "Windows 11 Client, version 24H2 and later (Servicing and Upgrade & Servicing Drivers)" {
		t.Errorf("productos mal: %q", r.Products)
	}
	if !r.Date.Equal(time.Date(2025, 12, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("fecha mal: %v", r.Date)
	}
	if hidden.Get("__VIEWSTATE") != "abc123==" || hidden.Get("__EVENTVALIDATION") != "ev&1" {
		t.Errorf("campos ocultos mal: %v", hidden)
	}
	if next != "ctl00$catalogBody$nextPageLinkText" {
		t.Errorf("siguiente mal: %q", next)
	}
}

func TestPickCandidate(t *testing.T) {
	rows, _, _ := parseCatalogHTML(catalogFixture)
	// Windows 11 24H2 (26100): 2.1.5.10 applies
	if c := pickCandidate(rows, 26100); c == nil || c.Version != "2.1.5.10" {
		t.Errorf("24H2 debería elegir 2.1.5.10: %+v", c)
	}
	// Windows 11 22H2 (22621): the 24H2 one is not compatible, leaving 2.1.5.7
	if c := pickCandidate(rows, 22621); c == nil || c.Version != "2.1.5.7" {
		t.Errorf("22H2 debería elegir 2.1.5.7: %+v", c)
	}
	// Firmware is never chosen even if it has a higher version.
	if c := pickCandidate(rows, 26100); c != nil && c.Version == "9.9.9.9" {
		t.Errorf("no debe elegir firmware")
	}
}

func TestProductCompatible(t *testing.T) {
	cases := []struct {
		p     string
		build int
		want  bool
	}{
		{"Windows 10 and later drivers", 19045, true},
		{"Windows 10 and later drivers", 26100, true},
		{"Windows 11 Client, version 24H2 and later (Servicing and Upgrade & Servicing Drivers)", 22631, false},
		{"Windows 11 Client, version 24H2 and later (Servicing and Upgrade & Servicing Drivers)", 26100, true},
		{"Windows 11 Client, version 22H2 and later", 19045, false},
		{"Windows 10, version 1903 and later, Servicing Drivers", 18362, true},
		{"Windows 10, version 1903 and later, Servicing Drivers", 17763, false},
		{"Windows 10, Vibranium and later, Servicing Drivers", 19045, true},
		{"Windows 10, Vibranium and later, Servicing Drivers", 18363, false},
		{"Windows 10 Anniversary Update and Later Servicing Drivers", 14393, true},
		{"Windows 8.1 and later drivers", 19045, true},
		{"Windows Server 2016 and later drivers", 19045, false},
		{"Windows 7", 19045, false},
		{"Windows 10 Anniversary Update and Later Servicing Drivers,Windows 10 Creators Update and Later Upgrade & Servicing Drivers", 19045, true},
	}
	for _, c := range cases {
		if got := productCompatible(c.p, c.build); got != c.want {
			t.Errorf("productCompatible(%q, %d) = %v, esperaba %v", c.p, c.build, got, c.want)
		}
	}
}

func TestCmpVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"1.2.10", "1.2.9", 1}, {"31.0.101.4502", "31.0.101.5333", -1},
		{"10.0.19041.1", "2.1.5.10", 1}, {"1.0", "1.0.0.0", 0}, {"abc", "1.0", 0}, {"2.1.5.10", "2.1.5.7", 1},
	}
	for _, c := range cases {
		if got := cmpVersion(c.a, c.b); got != c.want {
			t.Errorf("cmpVersion(%q,%q)=%d, esperaba %d", c.a, c.b, got, c.want)
		}
	}
}

func TestTrustedDownloadURL(t *testing.T) {
	ok := []string{
		"https://catalog.sf.dl.delivery.mp.microsoft.com/filestreamingservice/files/x/public/a.cab",
		"https://download.windowsupdate.com/d/msdownload/update/driver/drvs/2025/01/a.cab",
	}
	bad := []string{"http://download.windowsupdate.com/a.cab", "https://evil.example.com/a.cab",
		"https://microsoft.com.evil.net/a.cab", "https://notmicrosoft.com/a.cab"}
	for _, u := range ok {
		if !trustedDownloadURL(u) {
			t.Errorf("debería permitir %s", u)
		}
	}
	for _, u := range bad {
		if trustedDownloadURL(u) {
			t.Errorf("debería rechazar %s", u)
		}
	}
}

func TestDownloadRegex(t *testing.T) {
	page := `<script>downloadInformation[0].files[0].url = 'https://catalog.sf.dl.delivery.mp.microsoft.com/filestreamingservice/files/abc/public/foo_123.cab';</script>`
	m := dlURLRe.FindString(page)
	if m != "https://catalog.sf.dl.delivery.mp.microsoft.com/filestreamingservice/files/abc/public/foo_123.cab" {
		t.Errorf("regex mal: %q", m)
	}
}

func TestReduceHwIDAndAnalyze(t *testing.T) {
	devs := []rawDevice{
		{Name: "Intel(R) Ethernet Controller (3) I225-V", Class: "Net", DeviceId: `PCI\VEN_8086&DEV_15F3&SUBSYS_86941043&REV_03\4&1`,
			HwIds:    strList{`PCI\VEN_8086&DEV_15F3&SUBSYS_86941043&REV_03`, `PCI\VEN_8086&DEV_15F3`, `PCI\VEN_8086&CC_020000`},
			Provider: "Intel", Version: "1.1.4.43", Date: "2020-01-10", Signed: true},
		{Name: "Microsoft Basic Display Adapter", Class: "Display", DeviceId: `PCI\VEN_10DE&DEV_2504&SUBSYS_1&REV_A1\4&2`,
			HwIds: strList{`PCI\VEN_10DE&DEV_2504&SUBSYS_1&REV_A1`, `PCI\VEN_10DE&DEV_2504`}, Provider: "Microsoft", Version: "10.0.19041.1", Date: "2006-06-21", Signed: true},
		{Name: "Dispositivo roto", Class: "Other", DeviceId: `PCI\VEN_1234&DEV_5678\3&0`, HwIds: strList{`PCI\VEN_1234&DEV_5678`}, Problem: 28, Signed: true},
		{Name: "Realtek Audio", Class: "MEDIA", DeviceId: `HDAUDIO\FUNC_01&VEN_10EC&DEV_0256&SUBSYS_1&REV_1001\5&1`,
			HwIds: strList{`HDAUDIO\FUNC_01&VEN_10EC&DEV_0256&SUBSYS_10EC0256&REV_1001`}, Provider: "Realtek", Version: "6.0.9000.1", Date: "2024-05-01", Signed: true},
		{Name: "Volume", Class: "Volume", DeviceId: `STORAGE\VOLUME\1`},
		{Name: "USB Root Hub", Class: "USB", DeviceId: `USB\VID_8087&PID_0A2B\5`, HwIds: strList{`USB\VID_8087&PID_0A2B`}, Provider: "Microsoft", Signed: true},
		{Name: "Disabled thing", Class: "Net", DeviceId: `PCI\VEN_1&DEV_2\1`, Problem: 22},
	}
	if got := reduceHwID(devs[0]); got != `PCI\VEN_8086&DEV_15F3` {
		t.Errorf("reduceHwID: %q", got)
	}
	if got := reduceHwID(devs[3]); got != `HDAUDIO\FUNC_01&VEN_10EC&DEV_0256` {
		t.Errorf("reduceHwID audio: %q", got)
	}
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	rows, targets := analyzeDevices(devs, true, now)
	status := map[string]string{}
	for _, r := range rows {
		status[r.Name] = r.Status
	}
	want := map[string]string{
		"Intel(R) Ethernet Controller (3) I225-V": "old", "Microsoft Basic Display Adapter": "generic",
		"Dispositivo roto": "problem", "Realtek Audio": "ok",
	}
	for n, w := range want {
		if status[n] != w {
			t.Errorf("estado de %q = %q, esperaba %q", n, status[n], w)
		}
	}
	if _, ok := status["Volume"]; ok {
		t.Errorf("los volúmenes no son hardware")
	}
	if _, ok := status["USB Root Hub"]; ok {
		t.Errorf("los hubs USB no deben aparecer")
	}
	if len(targets) != 4 {
		t.Errorf("esperaba 4 objetivos de búsqueda (exhaustivo), hay %d: %v", len(targets), keysOf(targets))
	}
	_, t2 := analyzeDevices(devs, false, now)
	if len(t2) != 3 {
		t.Errorf("modo rápido: esperaba 3 objetivos (viejo, genérico, problema), hay %d: %v", len(t2), keysOf(t2))
	}
}

func keysOf(m map[string]*lookupTarget) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

func TestDecideOffer(t *testing.T) {
	c := &catRow{Version: "2.1.5.10", Title: "x"}
	mk := func(reasons []string, version, provider string) *lookupTarget {
		lt := &lookupTarget{Reasons: map[string]bool{}, Devices: []rawDevice{{Version: version, Provider: provider, Date: "2020-01-01"}}}
		for _, r := range reasons {
			lt.Reasons[r] = true
		}
		return lt
	}
	if ok, _, _ := decideOffer(mk([]string{"check"}, "2.1.5.10", "Intel"), c); ok {
		t.Errorf("misma versión: no ofrecer")
	}
	if ok, _, _ := decideOffer(mk([]string{"check"}, "2.1.5.7", "Intel"), c); !ok {
		t.Errorf("versión menor instalada: ofrecer")
	}
	if ok, _, _ := decideOffer(mk([]string{"check"}, "9.0.0.1", "Intel"), c); ok {
		t.Errorf("instalada más nueva: no ofrecer")
	}
	if ok, _, _ := decideOffer(mk([]string{"generic"}, "10.0.19041.1", "Microsoft"), c); !ok {
		t.Errorf("genérico: ofrecer")
	}
	if ok, _, from := decideOffer(mk([]string{"problem"}, "", ""), c); !ok || plainES(from) != "Sin controlador" {
		t.Errorf("problema: ofrecer (%v %q)", ok, from)
	}
}
