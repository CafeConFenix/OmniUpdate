package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const catalogBase = "https://www.catalog.update.microsoft.com"

type catRow struct {
	GUID, Title, Products, Classification, Version string
	Date                                           time.Time
}

type catalogClient struct {
	hc *http.Client
}

func newCatalogClient() *catalogClient {
	jar, _ := cookiejar.New(nil)
	return &catalogClient{hc: &http.Client{Timeout: 60 * time.Second, Jar: jar}}
}

func (c *catalogClient) do(ctx context.Context, method, u string, form url.Values) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := c.hc.Do(req)
		if err == nil {
			b, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == 200 {
				return string(b), nil
			}
			if rerr != nil {
				err = rerr
			} else {
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 1500 * time.Millisecond):
		}
	}
	return "", lastErr
}

var (
	guidRe     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	tdRe       = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
	inputRe    = regexp.MustCompile(`(?is)<input[^>]*>`)
	nextPageRe = regexp.MustCompile(`(?is)<a[^>]*id="[^"]*nextPage[^"]*"[^>]*>`)
	postBackRe = regexp.MustCompile(`__doPostBack\((?:'|&#39;|&quot;|")([^'"&]+)`)
)

func cellText(s string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, " ")), " "))
}

func attrOf(tag, name string) string {
	re := regexp.MustCompile(`(?is)\b` + name + `\s*=\s*("([^"]*)"|'([^']*)')`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	if m[2] != "" {
		return html.UnescapeString(m[2])
	}
	return html.UnescapeString(m[3])
}

func parseCatalogDate(s string) time.Time {
	parts := strings.Split(strings.TrimSpace(s), "/")
	if len(parts) != 3 {
		return time.Time{}
	}
	m, e1 := strconv.Atoi(parts[0])
	d, e2 := strconv.Atoi(parts[1])
	y, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return time.Time{}
	}
	if m > 12 {
		m, d = d, m
	}
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// parseCatalogHTML extracts the result rows from Search.aspx. It also returns
// the form's hidden fields and the target of the "next page" link.
func parseCatalogHTML(page string) (rows []catRow, hidden url.Values, next string) {
	chunks := regexp.MustCompile(`(?i)<tr[\s>]`).Split(page, -1)
	for _, ch := range chunks[1:] {
		g := guidRe.FindString(ch)
		if g == "" {
			continue
		}
		cells := tdRe.FindAllStringSubmatch(ch, -1)
		if len(cells) < 6 {
			continue
		}
		r := catRow{
			GUID:           strings.ToLower(g),
			Title:          cellText(cells[0][1]),
			Products:       cellText(cells[1][1]),
			Classification: cellText(cells[2][1]),
			Date:           parseCatalogDate(cellText(cells[3][1])),
			Version:        cellText(cells[4][1]),
		}
		if r.Title != "" {
			rows = append(rows, r)
		}
	}
	hidden = url.Values{}
	for _, tag := range inputRe.FindAllString(page, -1) {
		if strings.EqualFold(attrOf(tag, "type"), "hidden") {
			if n := attrOf(tag, "name"); n != "" {
				hidden.Set(n, attrOf(tag, "value"))
			}
		}
	}
	if tag := nextPageRe.FindString(page); tag != "" {
		if m := postBackRe.FindStringSubmatch(tag); m != nil {
			next = m[1]
		}
	}
	return
}

// searchBest looks up a hardware identifier and returns the best compatible
// driver. It only requests more result pages if the previous ones had no valid one.
func (c *catalogClient) searchBest(ctx context.Context, query string, build, maxPages int) (*catRow, string, error) {
	u := catalogBase + "/Search.aspx?q=" + url.QueryEscape(query)
	page, err := c.do(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	all, hidden, next := parseCatalogHTML(page)
	sample := ""
	low := strings.ToLower(page)
	if len(all) == 0 && !strings.Contains(low, "did not find any") && !strings.Contains(low, "no results") {
		sample = truncate(page, 600)
	}
	seen := map[string]bool{}
	for _, r := range all {
		seen[r.GUID] = true
	}
	best := pickCandidate(all, build)
	for p := 1; p < maxPages && best == nil && next != "" && len(all) >= 25*p; p++ {
		form := url.Values{}
		for k, v := range hidden {
			form[k] = v
		}
		form.Set("__EVENTTARGET", next)
		form.Set("__EVENTARGUMENT", "")
		pg, err := c.do(ctx, "POST", u, form)
		if err != nil {
			break
		}
		rows, h2, n2 := parseCatalogHTML(pg)
		added := 0
		for _, r := range rows {
			if !seen[r.GUID] {
				seen[r.GUID] = true
				all = append(all, r)
				added++
			}
		}
		if added == 0 {
			break
		}
		hidden, next = h2, n2
		best = pickCandidate(all, build)
	}
	return best, sample, nil
}

var dlURLRe = regexp.MustCompile(`https?://[^'"\s<>\\]+?\.(?:cab|msu|exe)`)

// downloadURL asks the catalog for the download address of an update.
func (c *catalogClient) downloadURL(ctx context.Context, guid string) (string, error) {
	body := `[{"size":0,"updateID":"` + guid + `","uidInfo":"` + guid + `"}]`
	page, err := c.do(ctx, "POST", catalogBase+"/DownloadDialog.aspx", url.Values{"updateIDs": {body}})
	if err != nil {
		return "", err
	}
	var cab, other string
	for _, m := range dlURLRe.FindAllString(page, -1) {
		if !trustedDownloadURL(m) {
			continue
		}
		if strings.HasSuffix(strings.ToLower(m), ".cab") {
			if cab == "" {
				cab = m
			}
		} else if other == "" {
			other = m
		}
	}
	if cab != "" {
		return cab, nil
	}
	if other != "" {
		return "", terr("el paquete no es un .cab de controladores", "the package is not a driver .cab")
	}
	return "", terr("el catálogo no devolvió ninguna descarga", "the catalog returned no download")
}

func trustedDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return strings.HasSuffix(h, ".microsoft.com") || strings.HasSuffix(h, ".windowsupdate.com") ||
		h == "download.windowsupdate.com"
}

func (c *catalogClient) download(ctx context.Context, rawURL, dest string) error {
	if !trustedDownloadURL(rawURL) {
		return terr("dirección de descarga no permitida", "download address not allowed")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	dl := &http.Client{Timeout: 30 * time.Minute}
	resp, err := dl.Do(req)
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
	n, err := io.Copy(f, io.LimitReader(resp.Body, 1<<30))
	f.Close()
	if err != nil {
		return err
	}
	if n == 0 {
		return terr("archivo vacío", "empty file")
	}
	return nil
}

// ---------------------------------------------------------------- versions

func parseVer(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(s, ".") {
		n := 0
		digits := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
			digits++
			if digits > 9 {
				break
			}
		}
		if digits == 0 {
			break
		}
		out = append(out, n)
	}
	return out
}

// cmpVersion compares two dotted versions (-1, 0, 1). If either is non-numeric it returns 0.
func cmpVersion(a, b string) int {
	x, y := parseVer(a), parseVer(b)
	if x == nil || y == nil {
		return 0
	}
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p < q {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---------------------------------------------------------------- compatibility

var (
	win10Builds = map[string]int{"1507": 10240, "1511": 10586, "1607": 14393, "1703": 15063, "1709": 16299,
		"1803": 17134, "1809": 17763, "1903": 18362, "1909": 18363, "2004": 19041, "20h2": 19042,
		"21h1": 19043, "21h2": 19044, "22h2": 19045}
	win11Builds = map[string]int{"21h2": 22000, "22h2": 22621, "23h2": 22631, "24h2": 26100, "25h2": 26200, "26h1": 28000}
	reWinSect   = regexp.MustCompile(`windows 1[01]`)
	reVerLater  = regexp.MustCompile(`version\s+([0-9]{4}|[0-9]{2}h[12])\s+and later`)
	reOldLater  = regexp.MustCompile(`windows (7|8|8\.1)[^,]*and later`)
)

var win10Names = []struct {
	name  string
	build int
}{{"vibranium", 19041}, {"anniversary update", 14393}, {"creators update", 15063},
	{"fall creators", 16299}, {"april 2018", 17134}, {"october 2018", 17763}, {"may 2019", 18362}}

// productCompatible reports whether the catalog's "Products" column includes the
// given Windows version (by build number).
func productCompatible(products string, build int) bool {
	p := strings.ToLower(products)
	if strings.Contains(p, "server") && !strings.Contains(p, "windows 10") && !strings.Contains(p, "windows 11") {
		return false
	}
	if reOldLater.MatchString(p) {
		return true
	}
	idx := reWinSect.FindAllStringIndex(p, -1)
	for i, m := range idx {
		end := len(p)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		sect := p[m[0]:end]
		is11 := strings.HasPrefix(sect, "windows 11")
		min := 10240
		if is11 {
			min = 22000
		}
		if v := reVerLater.FindStringSubmatch(sect); v != nil {
			table := win10Builds
			if is11 {
				table = win11Builds
			}
			if b, ok := table[v[1]]; ok {
				min = b
			}
		} else if !is11 {
			for _, n := range win10Names {
				if strings.Contains(sect, n.name) {
					min = n.build
					break
				}
			}
		}
		if build >= min {
			return true
		}
	}
	return false
}

// pickCandidate picks the newest version among the results that is compatible with this Windows.
func pickCandidate(rows []catRow, build int) *catRow {
	var best *catRow
	for i := range rows {
		r := &rows[i]
		cl := strings.ToLower(r.Classification)
		if !strings.HasPrefix(cl, "drivers") || strings.Contains(cl, "firmware") {
			continue
		}
		if parseVer(r.Version) == nil || !productCompatible(r.Products, build) {
			continue
		}
		if best == nil {
			best = r
			continue
		}
		if c := cmpVersion(r.Version, best.Version); c > 0 || (c == 0 && r.Date.After(best.Date)) {
			best = r
		}
	}
	return best
}

// ---------------------------------------------------------------- scanning

type catHit struct {
	key string
	row *catRow
	t   *lookupTarget
}

// collectCatalog queries the Microsoft Update Catalog for each device type (in parallel).
func (a *App) collectCatalog(ctx context.Context, targets map[string]*lookupTarget, build int) []catHit {
	a.stage("drivers", "running", "")
	if len(targets) == 0 {
		a.stage("drivers", "done", T("Sin dispositivos que comprobar", "No devices to check"))
		return nil
	}
	var keys []string
	for k := range targets {
		keys = append(keys, k)
	}
	a.logT("Controladores: buscando en el Catálogo de Microsoft Update (%d tipos de dispositivo)…", "Drivers: searching the Microsoft Update Catalog (%d device types)…", len(keys))
	cl := newCatalogClient()
	jobs := make(chan string)
	var mu sync.Mutex
	var hits []catHit
	failures, done := 0, 0
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range jobs {
				best, sample, err := cl.searchBest(ctx, k, build, 3)
				mu.Lock()
				done++
				d := done
				if err != nil {
					failures++
					a.diagf("catálogo %s: %v", k, err)
				} else {
					if sample != "" {
						a.diagf("catálogo %s: respuesta sin filas reconocibles: %s", k, sample)
					}
					if best != nil {
						hits = append(hits, catHit{key: k, row: best, t: targets[k]})
					}
				}
				mu.Unlock()
				a.stageFrac("drivers", float64(d)/float64(len(keys)), Tf("%d de %d dispositivos", "%d of %d devices", d, len(keys)))
			}
		}()
	}
	for _, k := range keys {
		select {
		case jobs <- k:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if failures > 0 {
		a.logT("Controladores: %d consulta(s) al catálogo fallaron (¿sin conexión?).", "Drivers: %d catalog quer(ies) failed (no connection?).", failures)
	}
	return hits
}

func (a *App) applyCatalogHits(hits []catHit, wuHwIDs []string) {
	byGUID := map[string]*Item{}
	var order []string
	a.mu.Lock()
	covered := map[string]bool{}
	for k, v := range a.vendorDevs {
		covered[k] = v
	}
	a.mu.Unlock()
	for _, h := range hits {
		dup := false
		for _, w := range wuHwIDs { // Windows Update already offers it: don't duplicate
			if strings.Contains(strings.ToUpper(w), h.key) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		// Devices already covered by the vendor: don't offer a second driver.
		t2 := *h.t
		t2.Devices = nil
		for _, d := range h.t.Devices {
			if !covered[strings.ToUpper(d.DeviceId)] {
				t2.Devices = append(t2.Devices, d)
			}
		}
		if len(t2.Devices) == 0 {
			continue
		}
		h.t = &t2
		offer, note, from := decideOffer(h.t, h.row)
		if !offer {
			continue
		}
		it := byGUID[h.row.GUID]
		if it == nil {
			grp := grpDrivers
			if h.t.Reasons["problem"] { // the device has no driver or doesn't work: something is missing
				grp = grpFaltan
			}
			it = &Item{ID: "cat:" + h.row.GUID, Kind: "catalog", Group: grp, Selectable: true,
				Detail: Tf("%s · %s · Catálogo de Microsoft Update", "%s · %s · Microsoft Update Catalog", h.row.Title, h.row.Date.Format("02/01/2006")),
				To:     h.row.Version, From: from, Note: note,
				data: map[string]string{"guid": h.row.GUID, "title": h.row.Title, "version": h.row.Version}}
			byGUID[h.row.GUID] = it
			order = append(order, h.row.GUID)
		}
		for _, d := range h.t.Devices {
			it.devs = append(it.devs, d.DeviceId)
			it.vers = append(it.vers, d.Version)
			if it.Name == "" {
				it.Name = strings.TrimSpace(d.Name)
			} else {
				it.Name += ", " + strings.TrimSpace(d.Name)
			}
		}
	}
	marked := map[string]string{}
	for _, g := range order {
		it := byGUID[g]
		if n := len(it.devs); n > 1 {
			names := strings.Split(it.Name, ", ")
			if len(names) > 2 {
				it.Name = Tf("%s y %d más", "%s and %d more", strings.Join(names[:2], ", "), len(names)-2)
			}
		}
		if it.Name == "" {
			it.Name = it.data["title"]
		}
		for _, id := range it.devs {
			marked[id] = it.To
		}
		a.addItem(it)
	}
	a.mu.Lock()
	for i := range a.devices {
		if v, ok := marked[a.devices[i].devID]; ok {
			a.devices[i].Status = "update"
			a.devices[i].Note = Tf("Hay una versión más nueva: %s", "A newer version is available: %s", v)
		}
	}
	a.mu.Unlock()
	a.stage("drivers", "done", Tf("%d controlador(es) más nuevos", "%d newer driver(s)", len(order)))
}

// decideOffer applies the rules for proposing (or not) a catalog driver.
func decideOffer(t *lookupTarget, c *catRow) (offer bool, note, from string) {
	lowest := ""
	var oldestDate time.Time
	anyMS := false
	for _, d := range t.Devices {
		if strings.Contains(strings.ToLower(d.Provider), "microsoft") {
			anyMS = true
		}
		if lowest == "" || (parseVer(d.Version) != nil && cmpVersion(d.Version, lowest) < 0) {
			lowest = d.Version
		}
		if dt, ok := parseISODate(d.Date); ok && (oldestDate.IsZero() || dt.Before(oldestDate)) {
			oldestDate = dt
		}
	}
	from = lowest
	switch {
	case t.Reasons["problem"]:
		return true, T("El dispositivo tiene un problema: este controlador puede resolverlo", "The device has a problem: this driver may fix it"), orGeneric(from)
	case t.Reasons["generic"]:
		return true, T("Sustituye al controlador genérico de Microsoft por uno del fabricante", "Replaces the generic Microsoft driver with the manufacturer's"), orGeneric(from)
	case t.Reasons["old"] && parseVer(from) == nil:
		return true, T("Controlador antiguo", "Old driver"), orGeneric(from)
	}
	if parseVer(from) == nil || parseVer(c.Version) == nil {
		return false, "", from
	}
	if cmpVersion(c.Version, from) > 0 {
		if anyMS {
			return true, T("Mejora el controlador genérico de Microsoft", "Improves on the generic Microsoft driver"), orGeneric(from)
		}
		return true, "", from
	}
	return false, "", from
}

func orGeneric(s string) string {
	if strings.TrimSpace(s) == "" {
		return T("Sin controlador", "No driver")
	}
	return s
}
