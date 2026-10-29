package main

import (
	"bytes"
	"context"
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
	"sync"
	"time"

	"omniupdate/internal/sys"
)

// GitHub apps: the published versions ("releases") are watched for the programs
// the user adds, and for installed programs whose Windows entry points to a
// GitHub repository. They are installed by downloading the release installer (MSI,
// .exe or portable .zip) only from GitHub domains.

// ------------------------------------------------------------ saved list

type ghEntry struct {
	Repo  string `json:"repo"`  // owner/name
	Tag   string `json:"tag"`   // installed version (empty: not installed yet)
	Added string `json:"added"` // date added
}

var (
	ghMu       sync.Mutex
	ghFileOver string // for tests
)

func ghStorePath() string {
	if ghFileOver != "" {
		return ghFileOver
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "OmniUpdate", "github-apps.json")
}

func loadGH() []ghEntry {
	ghMu.Lock()
	defer ghMu.Unlock()
	return loadGHLocked()
}

func loadGHLocked() []ghEntry {
	b, err := os.ReadFile(ghStorePath())
	if err != nil {
		return nil
	}
	var list []ghEntry
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	var out []ghEntry
	for _, e := range list {
		if _, ok := parseGithubRepo(e.Repo); ok {
			out = append(out, e)
		}
	}
	return out
}

func saveGHLocked(list []ghEntry) error {
	if err := os.MkdirAll(filepath.Dir(ghStorePath()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := ghStorePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ghStorePath())
}

func setGHTag(repo, tag string) {
	ghMu.Lock()
	defer ghMu.Unlock()
	list := loadGHLocked()
	for i := range list {
		if strings.EqualFold(list[i].Repo, repo) {
			list[i].Tag = tag
		}
	}
	_ = saveGHLocked(list)
}

// ------------------------------------------------------------ names and versions

var (
	ghOwnerRe  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	ghRepoRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	ghInTextRe = regexp.MustCompile(`(?i)github\.com[/:]([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))/([A-Za-z0-9._-]{1,100})`)
	tagNumRe   = regexp.MustCompile(`\d+(?:\.\d+)*`)
)

// parseGithubRepo accepts "owner/repo", a github.com URL or a git@github.com:… one.
func parseGithubRepo(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := ghInTextRe.FindStringSubmatch(s); m != nil && strings.Contains(strings.ToLower(s), "github.com") {
		return cleanRepo(m[1], m[2])
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) == 2 {
		return cleanRepo(parts[0], parts[1])
	}
	return "", false
}

func cleanRepo(owner, name string) (string, bool) {
	name = strings.TrimSuffix(name, ".git")
	if !ghOwnerRe.MatchString(owner) || !ghRepoRe.MatchString(name) || name == "." || name == ".." {
		return "", false
	}
	switch strings.ToLower(owner) {
	case "sponsors", "orgs", "apps", "marketplace", "topics", "settings", "features", "about", "pricing":
		return "", false
	}
	return owner + "/" + name, true
}

// tagVersion extracts the numeric part of a tag ("v1.2.3", "release-2.0", "ver_3.4.5-win").
func tagVersion(tag string) string { return tagNumRe.FindString(tag) }

// sameScheme avoids comparing different version schemes (e.g. 1.2.3 against 20260101).
func sameScheme(a, b string) bool {
	x, y := parseVer(a), parseVer(b)
	if len(x) == 0 || len(y) == 0 {
		return false
	}
	return (x[0] >= 1000) == (y[0] >= 1000)
}

// ------------------------------------------------------------ GitHub API

type ghAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

var ghHTTP = &http.Client{Timeout: 25 * time.Second}
var ghAPIBase = "https://api.github.com"

func ghLatest(ctx context.Context, repo string) (*ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", ghAPIBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "OmniUpdate/"+version)
	resp, err := ghHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 404:
		return nil, errNoRelease
	case 403, 429:
		return nil, terr("GitHub limitó las consultas (inténtalo de nuevo en un rato)", "GitHub rate-limited the requests (try again later)")
	default:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	if rel.Tag == "" || rel.Draft {
		return nil, errNoRelease
	}
	return &rel, nil
}

var errNoRelease = terr("el repositorio no tiene versiones publicadas (releases)", "the repository has no published releases")

// ------------------------------------------------------------ choosing the file

var assetBad = []string{"arm64", "aarch64", "armv7", "armhf", "linux", "darwin", "macos", "osx", "mac-", "-mac", "apple", "freebsd",
	"android", "ios-", ".deb", ".rpm", ".dmg", ".pkg", ".appimage", ".snap", ".sh", ".sig", ".asc", ".sha", ".sbom", ".txt", ".json",
	".yml", ".yaml", ".blockmap", "checksum", "source", "-src", "symbols", "debug", ".apk", ".nupkg", ".vsix", ".whl", ".tar", ".7z",
	".msix", ".appx", ".msixbundle", ".appxbundle", ".pdb", ".md", ".pem", ".minisig", ".intoto"}

// scoreAsset scores a release file; 0 = unusable. It also returns its type: msi | setup | portable | zip.
func scoreAsset(name string) (int, string) {
	l := strings.ToLower(name)
	for _, b := range assetBad {
		if strings.Contains(l, b) {
			return 0, ""
		}
	}
	win := strings.Contains(l, "win") || strings.Contains(l, "x64") || strings.Contains(l, "amd64") || strings.Contains(l, "x86_64")
	score, kind := 0, ""
	switch path.Ext(l) {
	case ".msi":
		score, kind = 100, "msi"
	case ".exe":
		if strings.Contains(l, "setup") || strings.Contains(l, "install") {
			score, kind = 90, "setup"
		} else {
			score, kind = 50, "portable"
		}
	case ".zip":
		if !win {
			return 0, ""
		}
		score, kind = 40, "zip"
		if strings.Contains(l, "portable") {
			score = 60
		}
	default:
		return 0, ""
	}
	if strings.Contains(l, "x64") || strings.Contains(l, "amd64") || strings.Contains(l, "x86_64") || strings.Contains(l, "win64") {
		score += 10
	} else if strings.Contains(l, "x86") || strings.Contains(l, "ia32") || strings.Contains(l, "i386") || strings.Contains(l, "win32") {
		score -= 20
	}
	if win {
		score += 5
	}
	return score, kind
}

// pickAsset picks the best file for 64-bit Windows; if the user has a pattern, it uses it.
func pickAsset(assets []ghAsset, pattern string) (*ghAsset, string) {
	var best *ghAsset
	bestScore, bestKind := 0, ""
	var re *regexp.Regexp
	if pattern != "" {
		re, _ = regexp.Compile("(?i)" + pattern)
	}
	for i := range assets {
		if re != nil && !re.MatchString(assets[i].Name) {
			continue
		}
		s, k := scoreAsset(assets[i].Name)
		if s > bestScore {
			best, bestScore, bestKind = &assets[i], s, k
		}
	}
	return best, bestKind
}

// ------------------------------------------------------------ search

func ghAllowedHost(h string) bool {
	h = strings.ToLower(h)
	return h == "github.com" || h == "api.github.com" || h == "objects.githubusercontent.com" || strings.HasSuffix(h, ".githubusercontent.com")
}

func (a *App) addGithubItem(repo string, rel *ghRelease, cur string, origin, displayName string) bool {
	as, kind := pickAsset(rel.Assets, "")
	owner := strings.SplitN(repo, "/", 2)
	name := displayName
	if name == "" {
		name = owner[1]
	}
	if as == nil {
		if origin == "tracked" {
			a.addItem(&Item{ID: "github:" + repo, Kind: "github", Group: grpGithub, Name: name, Detail: repo + " · GitHub",
				From: cur, To: rel.Tag,
				Note: T("No se encontró un instalador para Windows en la última versión; hay que instalarlo a mano.", "No Windows installer was found in the latest release; it must be installed by hand.")})
			return true
		}
		return false
	}
	note := ""
	switch kind {
	case "setup":
		note = T("Si el instalador no tiene modo silencioso, se abrirá su asistente.", "If the installer has no silent mode, its own wizard will open.")
	case "portable", "zip":
		note = T("Programa portátil: se copia a la carpeta de OmniUpdate (se indica la ruta al terminar).", "Portable program: it is copied to the OmniUpdate folder (the path is shown when it finishes).")
	}
	if origin == "discovered" {
		note = Join(" · ", T("Detectado por la web del programa (GitHub); comprueba que es el mismo", "Detected from the program's website (GitHub); check it is the same one"), note)
	}
	if as.Size > 0 {
		note = Join(" · ", fmt.Sprintf("%.0f MB", float64(as.Size)/1e6), note)
	}
	it := &Item{ID: "github:" + repo, Kind: "github", Group: grpGithub, Selectable: true, Name: name,
		Detail: repo + " · GitHub · " + as.Name, From: cur, To: rel.Tag, Note: note,
		data: map[string]string{"repo": repo, "tag": rel.Tag, "url": as.URL, "name": as.Name, "kind": kind, "origin": origin,
			"digest": strings.TrimPrefix(strings.ToLower(as.Digest), "sha256:")}}
	if cur == "" {
		it.From = T("No instalado", "Not installed")
		it.data["mode"] = "install"
	}
	a.addItem(it)
	return true
}

func (a *App) scanGithub(ctx context.Context, apps []rawApp) int {
	tracked := loadGH()
	trackedSet := map[string]bool{}
	for _, t := range tracked {
		trackedSet[strings.ToLower(t.Repo)] = true
	}
	type job struct {
		repo, cur, origin, name string
	}
	var jobs []job
	for _, t := range tracked {
		jobs = append(jobs, job{t.Repo, t.Tag, "tracked", ""})
	}
	// installed programs whose entry points to GitHub
	seen := map[string]bool{}
	nDisc := 0
	for _, p := range apps {
		if nDisc >= 25 || p.Urls == "" || parseVer(p.Version) == nil {
			continue
		}
		m := ghInTextRe.FindStringSubmatch(p.Urls)
		if m == nil {
			continue
		}
		repo, ok := cleanRepo(m[1], m[2])
		if !ok || seen[strings.ToLower(repo)] || trackedSet[strings.ToLower(repo)] {
			continue
		}
		seen[strings.ToLower(repo)] = true
		jobs = append(jobs, job{repo, p.Version, "discovered", p.Name})
		nDisc++
	}
	if len(jobs) == 0 {
		return 0
	}
	type res struct {
		rel *ghRelease
		err error
	}
	results := make([]res, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, j job) {
			defer wg.Done()
			defer func() { <-sem }()
			rel, err := ghLatest(ctx, j.repo)
			results[i] = res{rel, err}
		}(i, j)
	}
	wg.Wait()
	n := 0
	for i, j := range jobs {
		r := results[i]
		if r.err != nil {
			a.diagf("github %s: %v", j.repo, r.err)
			if j.origin == "tracked" && r.err != errNoRelease {
				a.addItem(&Item{ID: "warn:gh:" + j.repo, Kind: "info", Group: grpAtencion, Name: j.repo,
					Detail: T("Programa de GitHub", "GitHub program"), Note: Cat(T("No se pudo consultar: ", "Could not check: "), errText(r.err))})
			}
			continue
		}
		latest := tagVersion(r.rel.Tag)
		switch {
		case j.origin == "tracked" && j.cur == "":
			// not installed yet
		case j.origin == "tracked" && parseVer(tagVersion(j.cur)) != nil && parseVer(latest) != nil:
			if cmpVersion(latest, tagVersion(j.cur)) <= 0 {
				continue
			}
		case j.origin == "tracked":
			if j.cur == r.rel.Tag {
				continue
			}
		default: // discovered: only if clearly newer
			if parseVer(latest) == nil || !sameScheme(latest, j.cur) || cmpVersion(latest, j.cur) <= 0 {
				continue
			}
		}
		cur := j.cur
		if j.origin == "tracked" && cur != "" {
			cur = j.cur
		}
		if a.addGithubItem(j.repo, r.rel, cur, j.origin, j.name) {
			n++
		}
	}
	return n
}

// ghAdd adds a program to the watched list (checking that it exists and publishes releases).
func ghAdd(ctx context.Context, input string, installed bool) (ghEntry, error) {
	repo, ok := parseGithubRepo(input)
	if !ok {
		return ghEntry{}, terr("Escribe el repositorio como «propietario/nombre» o pega su dirección de github.com", "Type the repository as “owner/name” or paste its github.com address")
	}
	rel, err := ghLatest(ctx, repo)
	if err != nil {
		return ghEntry{}, err
	}
	e := ghEntry{Repo: repo, Added: time.Now().Format("2006-01-02")}
	if installed {
		e.Tag = rel.Tag
	}
	ghMu.Lock()
	defer ghMu.Unlock()
	list := loadGHLocked()
	for i := range list {
		if strings.EqualFold(list[i].Repo, repo) {
			list[i].Tag = e.Tag
			return list[i], saveGHLocked(list)
		}
	}
	list = append(list, e)
	return e, saveGHLocked(list)
}

func ghRemove(repo string) error {
	ghMu.Lock()
	defer ghMu.Unlock()
	list := loadGHLocked()
	var out []ghEntry
	for _, e := range list {
		if !strings.EqualFold(e.Repo, repo) {
			out = append(out, e)
		}
	}
	return saveGHLocked(out)
}

// dedupeGithub removes "discovered" GitHub proposals that winget already offers.
func (a *App) dedupeGithub() {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var wg []string
	for _, it := range a.items {
		if it.Kind == "winget" {
			wg = append(wg, norm(plainES(it.Name)), norm(strings.TrimSpace(strings.SplitN(plainES(it.Detail), " · ", 2)[0])))
		}
	}
	kept := a.items[:0]
	for _, it := range a.items {
		if it.Kind == "github" && it.data["origin"] == "discovered" {
			n := norm(plainES(it.Name))
			repoName := norm(strings.SplitN(it.data["repo"], "/", 2)[1])
			dup := false
			for _, w := range wg {
				if w == "" {
					continue
				}
				if (len(n) >= 4 && (strings.Contains(w, n) || strings.Contains(n, w))) || (len(repoName) >= 4 && strings.HasSuffix(w, repoName)) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		}
		kept = append(kept, it)
	}
	a.items = kept
}

// ------------------------------------------------------------ installation

// detectInstaller recognizes the installer type of an .exe by the markers left by Inno Setup, NSIS and WiX Burn.
func detectInstaller(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	marks := []struct{ kind, mark string }{{"inno", "Inno Setup Setup Data"}, {"nsis", "Nullsoft Install System"}, {"burn", ".wixburn"}}
	buf := make([]byte, 4<<20)
	overlap := 64
	var carry []byte
	var total int64
	for total < 400<<20 {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := append(carry, buf[:n]...)
			for _, m := range marks {
				if bytes.Contains(chunk, []byte(m.mark)) {
					return m.kind
				}
			}
			if len(chunk) > overlap {
				carry = append([]byte{}, chunk[len(chunk)-overlap:]...)
			} else {
				carry = append([]byte{}, chunk...)
			}
			total += int64(n)
		}
		if err != nil {
			break
		}
	}
	return ""
}

func silentArgs(kind string) []string {
	switch kind {
	case "inno":
		return []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-"}
	case "nsis":
		return []string{"/S"}
	case "burn":
		return []string{"/quiet", "/norestart"}
	}
	return nil
}

func appsDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Programs", "OmniUpdate", "apps")
}

func (a *App) installGithub(ctx context.Context, it *Item) (bool, string, bool) {
	d := it.data
	repo, tag, kind := d["repo"], d["tag"], d["kind"]
	if _, ok := parseGithubRepo(repo); !ok {
		return false, T("Repositorio no válido", "Invalid repository"), false
	}
	u, err := url.Parse(d["url"])
	if err != nil || u.Scheme != "https" || !ghAllowedHost(u.Hostname()) {
		return false, T("Dirección de descarga no permitida", "Download address not allowed"), false
	}
	fname := filepath.Base(d["name"])
	if fname == "." || fname == string(filepath.Separator) || strings.ContainsAny(fname, `<>:"|?*`) {
		return false, T("Nombre de archivo no válido", "Invalid file name"), false
	}
	dir := filepath.Join(tempDir, "gh", fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, errText(err), false
	}
	defer os.RemoveAll(dir)
	dl := filepath.Join(dir, fname)
	a.setItem(it, "working", T("Descargando…", "Downloading…"))
	if err := downloadAllowed(ctx, d["url"], dl, ghAllowedHost); err != nil {
		return false, Cat(T("Falló la descarga: ", "Download failed: "), errText(err)), false
	}
	if want := d["digest"]; len(want) == 64 {
		got, err := fileSHA256(dl)
		if err != nil || !strings.EqualFold(got, want) {
			a.diagf("SHA-256 de %s: esperado %s, obtenido %s (%v)", fname, want, got, err)
			return false, T("La descarga no coincide con la huella (SHA-256) publicada por GitHub: no se instala", "The download does not match the SHA-256 fingerprint published by GitHub: it will not be installed"), false
		}
	}
	if kind == "msi" || kind == "setup" || kind == "portable" {
		sig := runPS(ctx, 3*time.Minute, signatureScript, dl)
		a.diagf("firma de %s: %s", fname, strings.TrimSpace(sig.Out))
		if strings.HasPrefix(strings.TrimSpace(sig.Out), "HashMismatch") {
			return false, T("El archivo está alterado (la firma digital no coincide): no se instala", "The file has been tampered with (the digital signature does not match): it will not be installed"), false
		}
	}
	a.setItem(it, "working", T("Instalando…", "Installing…"))
	reboot := false
	var msg string
	switch kind {
	case "msi":
		r := runCmd(ctx, 45*time.Minute, sys.Command("msiexec.exe", "/i", dl, "/qn", "/norestart"))
		a.diagf("=== msiexec %s (código %d) ===\n%s", fname, r.Exit, truncate(r.Out, 2000))
		switch r.Exit {
		case 0:
			msg = T("Instalado", "Installed")
		case 3010, 1641:
			msg, reboot = T("Instalado (hay que reiniciar para terminarlo)", "Installed (restart to finish)"), true
		default:
			return false, Tf("msiexec terminó con el código %d", "msiexec finished with code %d", r.Exit), false
		}
	case "setup":
		det := detectInstaller(dl)
		args := silentArgs(det)
		var r cmdResult
		if args != nil {
			r = runCmd(ctx, 45*time.Minute, sys.Command(dl, args...))
			msg = T("Instalado", "Installed")
		} else {
			r = runCmd(ctx, 45*time.Minute, sys.CommandVisible(dl))
			msg = T("Instalado con el asistente del programa", "Installed with the program's own wizard")
		}
		a.diagf("=== %s tipo=%q (código %d) ===\n%s", fname, det, r.Exit, truncate(r.Out, 2000))
		switch {
		case r.Timed:
			return false, T("La instalación tardó demasiado y se canceló", "The installation took too long and was cancelled"), false
		case r.Exit == 3010 || r.Exit == 1641:
			msg, reboot = T("Instalado (hay que reiniciar para terminarlo)", "Installed (restart to finish)"), true
		case r.Exit != 0:
			return false, Tf("El instalador terminó con el código %d", "The installer finished with code %d", r.Exit), false
		}
	case "portable", "zip":
		dest := filepath.Join(appsDir(), strings.ReplaceAll(repo, "/", "-"))
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return false, errText(err), false
		}
		if kind == "zip" {
			if err := unzipSafe(dl, dest); err != nil {
				return false, Cat(T("No se pudo descomprimir (¿el programa está abierto?): ", "Could not unpack (is the program running?): "), errText(err)), false
			}
		} else {
			b, err := os.ReadFile(dl)
			if err == nil {
				err = os.WriteFile(filepath.Join(dest, fname), b, 0o755)
			}
			if err != nil {
				return false, Cat(T("No se pudo copiar (¿el programa está abierto?): ", "Could not copy (is the program running?): "), errText(err)), false
			}
		}
		msg = Tf("Instalado en %s", "Installed in %s", dest)
	default:
		return false, T("Tipo de archivo no admitido", "Unsupported file type"), false
	}
	if d["origin"] == "tracked" {
		setGHTag(repo, tag)
	}
	return true, msg, reboot
}

var _ = io.EOF
var _ = sort.Strings
