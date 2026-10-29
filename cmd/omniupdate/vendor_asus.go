package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"omniupdate/internal/sys"
)

// ASUS driver catalog. The ASUS support website publishes, for each model, the list of
// packages with: the hardware identifiers they cover (HardwareInfoList), INF date, SHA-256
// fingerprint and, above all, the silent command ASUS specifies to install them (ExeModule).
// This allows installing them without windows and without the user having to download anything.

func hostIs(h string) func(string) bool {
	return func(x string) bool { return strings.EqualFold(x, h) }
}

func hostSuffix(suffix string) func(string) bool {
	s := strings.ToLower(suffix)
	return func(x string) bool {
		x = strings.ToLower(x)
		return x == strings.TrimPrefix(s, ".") || strings.HasSuffix(x, s)
	}
}

// downloadAllowed downloads a file over HTTPS only if the domain (and each redirect) is allowed.
func downloadAllowed(ctx context.Context, raw, dest string, allow func(string) bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !allow(u.Hostname()) {
		return terr("dirección de descarga no permitida", "download address not allowed")
	}
	client := &http.Client{
		Timeout: 45 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return terr("demasiadas redirecciones", "too many redirects")
			}
			if req.URL.Scheme != "https" || !allow(req.URL.Hostname()) {
				return terr("redirección a un sitio no permitido", "redirect to a site that is not allowed")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 2<<30))
	f.Close()
	if err != nil {
		return err
	}
	if n == 0 {
		return terr("archivo vacío", "empty file")
	}
	return nil
}

// ------------------------------------------------ reading the catalog

type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	var str string
	if json.Unmarshal(b, &str) == nil {
		*f = flexStr(strings.TrimSpace(str))
		return nil
	}
	*f = flexStr(strings.Trim(s, `"`))
	return nil
}

type asusFile struct {
	ID          flexStr `json:"Id"`
	Version     flexStr
	Title       flexStr
	FileSize    flexStr
	ReleaseDate flexStr
	DownloadUrl struct{ Global, China flexStr }
	ExeModule   flexStr
	Reboot      flexStr
	Sha256      flexStr `json:"sha256"`
	INFDate     flexStr
	// Each element is usually {"hardwareid": "...", "date": null, "version": null}.
	HardwareInfoList []json.RawMessage
}

type asusResp struct {
	Status flexStr
	Result *struct {
		Obj []struct {
			Name  flexStr
			Files []asusFile
		}
	}
}

type feedPkg struct {
	Vendor, Category, Title, Version string
	InfDate                          time.Time
	URL, SHA256, Exe, ExeArgs        string
	Reboot                           bool
	Size                             string
	HwIDs                            []string
}

func (f asusFile) hwIDs() []string {
	var out []string
	for _, raw := range f.HardwareInfoList {
		var o struct {
			Hardwareid flexStr `json:"hardwareid"`
		}
		if json.Unmarshal(raw, &o) == nil && o.Hardwareid != "" {
			out = append(out, strings.ToUpper(strings.TrimSpace(string(o.Hardwareid))))
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			out = append(out, strings.ToUpper(strings.TrimSpace(s)))
		}
	}
	return out
}

func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "Vv")
	if f := strings.Fields(v); len(f) > 0 {
		return f[0]
	}
	return ""
}

func parseFeedDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"01-02-2006", "2006-01-02", "2006/01/02", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

var (
	safeExeRe  = regexp.MustCompile(`^[A-Za-z0-9_.\-() ]+\.[eE][xX][eE]$`)
	safeArgRe  = regexp.MustCompile(`^[/\-][A-Za-z0-9_=:.\-]+$`)
	sigValidRe = regexp.MustCompile(`(?m)^Valid\|`)
)

// parseSilentArgs validates the parameters given by the catalog (simple options only, no paths or commands).
func parseSilentArgs(s string) ([]string, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil, false
	}
	for _, a := range f {
		if !safeArgRe.MatchString(a) {
			return nil, false
		}
	}
	return f, true
}

var asusDownloadHost = hostSuffix(".asus.com")

func skipCategory(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "bios") || strings.Contains(n, "firmware")
}

func asusPkg(category string, f asusFile) (feedPkg, bool) {
	parts := strings.SplitN(string(f.ExeModule), "%%", 2)
	exe := strings.TrimSpace(parts[0])
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	if !safeExeRe.MatchString(exe) {
		return feedPkg{}, false // .bat installers, Microsoft Store, etc.: not installed silently
	}
	if _, ok := parseSilentArgs(args); !ok {
		return feedPkg{}, false
	}
	ver := cleanVersion(string(f.Version))
	if parseVer(ver) == nil {
		return feedPkg{}, false
	}
	u, err := url.Parse(string(f.DownloadUrl.Global))
	if err != nil || u.Scheme != "https" || !asusDownloadHost(u.Hostname()) {
		return feedPkg{}, false
	}
	ext := strings.ToLower(path.Ext(u.Path))
	if ext != ".exe" && ext != ".zip" {
		return feedPkg{}, false
	}
	hw := f.hwIDs()
	if len(hw) == 0 {
		return feedPkg{}, false // without hardware identifiers there's no way to know whether it's needed
	}
	rb := strings.EqualFold(string(f.Reboot), "1") || strings.EqualFold(string(f.Reboot), "true")
	return feedPkg{Vendor: "ASUS", Category: category, Title: string(f.Title), Version: ver,
		InfDate: parseFeedDate(string(f.INFDate)), URL: string(f.DownloadUrl.Global),
		SHA256: strings.ToUpper(string(f.Sha256)), Exe: exe, ExeArgs: args, Reboot: rb,
		Size: string(f.FileSize), HwIDs: hw}, true
}

func parseAsusDrivers(body []byte) ([]feedPkg, error) {
	var resp asusResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("respuesta sin resultados (%s)", resp.Status)
	}
	var pkgs []feedPkg
	for _, cat := range resp.Result.Obj {
		if skipCategory(string(cat.Name)) {
			continue
		}
		for _, f := range cat.Files {
			if p, ok := asusPkg(string(cat.Name), f); ok {
				pkgs = append(pkgs, p)
			}
		}
	}
	return pkgs, nil
}

// ------------------------------------------------ identifying the model

var asusTokRe = regexp.MustCompile(`^[A-Za-z]{1,4}[0-9]{2,4}[A-Za-z0-9\-]*$`)

func genericModelName(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return l == "" || strings.Contains(l, "system product name") || strings.Contains(l, "to be filled") || strings.Contains(l, "default string")
}

func asusModelCandidates(model, board string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToUpper(s)] {
			return
		}
		seen[strings.ToUpper(s)] = true
		out = append(out, s)
	}
	sep := func(r rune) bool { return r == ' ' || r == '_' }
	slugs := func() {
		if !genericModelName(board) {
			slug := strings.Join(strings.FieldsFunc(board, sep), "-")
			add(strings.ToUpper(slug))
			add(strings.ToLower(slug))
		}
	}
	if genericModelName(model) {
		slugs() // custom-built PC: the useful model is the motherboard's
	}
	var toks []string
	for _, src := range []string{model, board} {
		if genericModelName(src) {
			continue
		}
		for _, tok := range strings.FieldsFunc(src, sep) {
			if len(tok) >= 4 && asusTokRe.MatchString(tok) && hasDigit(tok) {
				toks = append(toks, strings.ToUpper(tok))
			}
		}
	}
	for _, t := range toks {
		add(t)
	}
	slugs()
	for _, t := range toks { // variant without the last letter or digit (FX507ZC4 → FX507ZC)
		if len(t) >= 6 {
			add(t[:len(t)-1])
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

var asusHosts = []string{
	"https://rog.asus.com/support/webapi/product/GetPDDrivers",
	"https://www.asus.com/support/webapi/product/GetPDDrivers",
	"https://www.asus.com/support/api/product.asmx/GetPDDrivers",
}

func httpGetBody(ctx context.Context, hc *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return b, nil
}

func (a *App) fetchASUS(ctx context.Context, cands []string, osids []int) ([]feedPkg, string) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	hc := &http.Client{Timeout: 20 * time.Second}
	for _, osid := range osids {
		for _, m := range cands {
			for _, base := range asusHosts {
				if ctx.Err() != nil {
					return nil, ""
				}
				u := fmt.Sprintf("%s?website=global&model=%s&osid=%d", base, url.QueryEscape(m), osid)
				body, err := httpGetBody(ctx, hc, u)
				if err != nil {
					a.diagf("ASUS %s → error de red: %v", u, err)
					continue
				}
				pk, err := parseAsusDrivers(body)
				a.diagf("ASUS %s → %d paquetes instalables en silencio (%v)", u, len(pk), err)
				if err == nil && len(pk) > 0 {
					return pk, m
				}
			}
		}
	}
	return nil, ""
}

// ------------------------------------------------ comparing with what is installed

// needsUpdate decides whether the package is newer than the driver the device has.
// It uses the INF date and, if they can be compared, also the version numbers; when in doubt
// it proposes nothing (so the same thing isn't offered over and over).
func needsUpdate(d rawDevice, p feedPkg) bool {
	if d.Problem == 28 || (strings.TrimSpace(d.Version) == "" && strings.TrimSpace(d.Date) == "") {
		return true // device without a driver
	}
	instDate, okD := parseISODate(d.Date)
	dateKnown := okD && !p.InfDate.IsZero()
	comparable := parseVer(d.Version) != nil && parseVer(p.Version) != nil
	cmp := 0
	if comparable {
		cmp = cmpVersion(d.Version, p.Version)
	}
	switch {
	case dateKnown && !instDate.Before(p.InfDate):
		return false
	case comparable && cmp >= 0:
		return false
	case dateKnown:
		return true
	case comparable:
		return true
	}
	return false
}

type vendorHit struct {
	P    feedPkg
	Devs []rawDevice
}

func newerPkg(a, b feedPkg) bool {
	if c := cmpVersion(a.Version, b.Version); c != 0 {
		return c > 0
	}
	return a.InfDate.After(b.InfDate)
}

// matchFeed matches the vendor's packages against the computer's devices.
func matchFeed(pkgs []feedPkg, devs []rawDevice) []vendorHit {
	idx := map[string][]int{}
	for i, d := range devs {
		for _, h := range d.HwIds {
			k := strings.ToUpper(strings.TrimSpace(h))
			idx[k] = append(idx[k], i)
		}
	}
	type cand struct {
		p    feedPkg
		devs map[int]bool
	}
	groups := map[string]*cand{}
	for _, p := range pkgs {
		set := map[int]bool{}
		for _, h := range p.HwIDs {
			for _, i := range idx[h] {
				set[i] = true
			}
		}
		if len(set) == 0 {
			continue
		}
		k := p.Category + "|" + p.Title
		if g := groups[k]; g == nil || newerPkg(p, g.p) {
			groups[k] = &cand{p, set}
		}
	}
	var hits []vendorHit
	for _, g := range groups {
		var need []rawDevice
		var ids []int
		for i := range g.devs {
			ids = append(ids, i)
		}
		sort.Ints(ids)
		for _, i := range ids {
			if needsUpdate(devs[i], g.p) {
				need = append(need, devs[i])
			}
		}
		if len(need) > 0 {
			hits = append(hits, vendorHit{P: g.p, Devs: need})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].P.Category != hits[j].P.Category {
			return hits[i].P.Category < hits[j].P.Category
		}
		return hits[i].P.Title < hits[j].P.Title
	})
	return hits
}

func (a *App) scanASUS(ctx context.Context, inv *invRaw, si SystemInfo) {
	if inv == nil {
		a.stage("fabricante", "done", T("ASUS: no se pudieron leer los dispositivos", "ASUS: could not read the devices"))
		return
	}
	cands := asusModelCandidates(si.Model, si.Board)
	osids := []int{45, 52}
	if si.Build >= 22000 {
		osids = []int{52, 45}
	}
	a.logT("Fabricante: consultando el catálogo de drivers de ASUS (%s)…", "Manufacturer: querying the ASUS driver catalog (%s)…", strings.Join(cands, ", "))
	a.diagf("ASUS: modelo=%q placa=%q candidatos=%v", si.Model, si.Board, cands)
	pk, model := a.fetchASUS(ctx, cands, osids)
	if len(pk) == 0 {
		a.logT("Fabricante: ASUS no devolvió drivers para este modelo; se usan Windows Update y el Catálogo de Microsoft.", "Manufacturer: ASUS returned no drivers for this model; Windows Update and the Microsoft Catalog are used.")
		a.stage("fabricante", "done", T("ASUS: sin catálogo para este modelo (se usan Windows Update y el Catálogo de Microsoft)", "ASUS: no catalog for this model (Windows Update and the Microsoft Catalog are used)"))
		return
	}
	hits := matchFeed(pk, inv.Devices)
	var covered []string
	for _, h := range hits {
		it := &Item{ID: "vendor:asus:" + h.P.Category + "|" + h.P.Title + "|" + h.P.Version, Kind: "vendor", Group: grpFabr,
			Selectable: true, Name: h.P.Title, To: h.P.Version,
			data: map[string]string{"vendor": "ASUS", "url": h.P.URL, "sha256": h.P.SHA256, "exe": h.P.Exe,
				"args": h.P.ExeArgs, "reboot": fmt.Sprint(h.P.Reboot)}}
		var names []string
		for _, d := range h.Devs {
			it.devs = append(it.devs, d.DeviceId)
			it.vers = append(it.vers, d.Version)
			covered = append(covered, d.DeviceId)
			if n := strings.TrimSpace(d.Name); n != "" {
				names = append(names, n)
			}
			if it.From == "" || (parseVer(d.Version) != nil && cmpVersion(d.Version, it.From) < 0) {
				it.From = d.Version
			}
		}
		if strings.TrimSpace(it.From) == "" {
			it.From = T("Sin controlador", "No driver")
		}
		it.Detail = "ASUS · " + h.P.Category
		if len(names) > 0 {
			it.Detail += " · " + names[0]
			if len(names) > 1 {
				it.Detail = Cat(it.Detail, Tf(" y %d más", " and %d more", len(names)-1))
			}
		}
		var notes []string
		if h.P.Size != "" {
			notes = append(notes, h.P.Size)
		}
		if h.P.Reboot {
			notes = append(notes, T("pedirá reiniciar", "will ask to restart"))
		}
		it.Note = Join(" · ", notes...)
		a.addItem(it)
	}
	a.markVendorDevs(covered)
	a.logT("Fabricante: ASUS %s: %d driver(s) más nuevos.", "Manufacturer: ASUS %s: %d newer driver(s).", model, len(hits))
	a.stage("fabricante", "done", Tf("ASUS %s: %d driver(s) más nuevos", "ASUS %s: %d newer driver(s)", model, len(hits)))
}

// ------------------------------------------------ installation

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

// unzipSafe extracts a ZIP, rejecting paths that escape the destination folder.
func unzipSafe(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > 20000 {
		return terr("el paquete tiene demasiados archivos", "the package has too many files")
	}
	root, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	var total uint64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		target := filepath.Join(root, filepath.FromSlash(name))
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return terr("el paquete contiene una ruta no permitida", "the package contains a path that is not allowed")
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		total += f.UncompressedSize64
		if total > 4<<30 {
			return terr("el paquete es demasiado grande", "the package is too large")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func findFile(dir, name string) string {
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), name) && found == "" {
			found = p
		}
		return nil
	})
	return found
}

const signatureScript = `$s = Get-AuthenticodeSignature -LiteralPath $args[0]; "$($s.Status)|$($s.SignerCertificate.Subject)"`

// installVendor downloads the vendor's package, verifies its fingerprint and digital signature and
// installs it with the silent parameters the vendor itself publishes.
func (a *App) installVendor(ctx context.Context, it *Item) (ok bool, msg string, reboot bool) {
	d := it.data
	if d["vendor"] != "ASUS" {
		return false, T("Fabricante no admitido", "Manufacturer not supported"), false
	}
	exeName := d["exe"]
	argv, valid := parseSilentArgs(d["args"])
	if !safeExeRe.MatchString(exeName) || !valid {
		return false, T("Parámetros de instalación no válidos", "Invalid installation parameters"), false
	}
	u, err := url.Parse(d["url"])
	if err != nil {
		return false, T("Dirección de descarga no válida", "Invalid download address"), false
	}
	dir := filepath.Join(tempDir, "vendor", fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err.Error(), false
	}
	defer os.RemoveAll(dir)

	isZip := strings.EqualFold(path.Ext(u.Path), ".zip")
	dl := filepath.Join(dir, "paquete.bin")
	if err := downloadAllowed(ctx, d["url"], dl, asusDownloadHost); err != nil {
		return false, Cat(T("Falló la descarga: ", "Download failed: "), errText(err)), false
	}
	if want := d["sha256"]; want != "" {
		got, err := fileSHA256(dl)
		if err != nil || !strings.EqualFold(got, want) {
			a.diagf("SHA-256 de %s: esperado %s, obtenido %s (%v)", it.Name, want, got, err)
			return false, T("La descarga no coincide con la huella (SHA-256) publicada por el fabricante: no se instala", "The download does not match the SHA-256 fingerprint published by the manufacturer: it will not be installed"), false
		}
	}
	a.setItem(it, "working", T("Instalando…", "Installing…"))
	var exePath string
	if isZip {
		ex := filepath.Join(dir, "x")
		if err := unzipSafe(dl, ex); err != nil {
			return false, Cat(T("No se pudo descomprimir el paquete: ", "Could not unpack the package: "), errText(err)), false
		}
		if exePath = findFile(ex, exeName); exePath == "" {
			return false, T("El paquete no contiene el instalador esperado", "The package does not contain the expected installer"), false
		}
	} else {
		exePath = filepath.Join(dir, exeName)
		if err := os.Rename(dl, exePath); err != nil {
			return false, err.Error(), false
		}
	}
	sig := runPS(ctx, 3*time.Minute, signatureScript, exePath)
	if !sigValidRe.MatchString(sig.Out) {
		a.diagf("firma de %s: %s", exePath, strings.TrimSpace(sig.Out))
		return false, T("El instalador no tiene una firma digital válida: por seguridad no se ejecuta", "The installer has no valid digital signature: for safety it is not run"), false
	}
	cmd := sys.Command(exePath, argv...)
	cmd.Dir = filepath.Dir(exePath)
	r := runCmd(ctx, 45*time.Minute, cmd)
	a.diagf("=== %s %s (código %d) ===\n%s", exeName, strings.Join(argv, " "), r.Exit, truncate(r.Out, 3000))
	needReboot := d["reboot"] == "true"
	switch {
	case r.Timed:
		return false, T("La instalación tardó demasiado y se canceló", "The installation took too long and was cancelled"), false
	case r.Exit == 0:
		return true, T("Instalado", "Installed"), needReboot
	case r.Exit == 3010 || r.Exit == 1641:
		return true, T("Instalado (hay que reiniciar para terminarlo)", "Installed (restart to finish)"), true
	}
	return false, Tf("El instalador terminó con el código %d", "The installer finished with code %d", r.Exit), false
}
