package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseExtensions(t *testing.T) {
	out := "Installed extensions:\r\nms-python.python@2024.14.1\r\nesbenp.prettier-vscode@10.1.0\r\nms-vscode.cpptools@1.22.0@win32-x64\r\nMS-Python.Python@2024.14.1\r\nruido sin version\r\n"
	rows := parseExtensions(out)
	if len(rows) != 3 {
		t.Fatalf("se esperaban 3 extensiones y hay %d: %+v", len(rows), rows)
	}
	if rows[0].ID != "ms-python.python" || rows[0].Version != "2024.14.1" || rows[2].Version != "1.22.0" {
		t.Errorf("lectura incorrecta: %+v", rows)
	}
}

func TestMarketplaceVersion(t *testing.T) {
	body := []byte(`{"results":[{"extensions":[{"versions":[
	 {"version":"2025.5.0","properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]},
	 {"version":"2025.4.0","properties":[{"key":"Microsoft.VisualStudio.Code.Engine","value":"^1.99.0"}]},
	 {"version":"2025.2.0","properties":[{"key":"Microsoft.VisualStudio.Code.Engine","value":"^1.90.0"}]}]}]}]}`)
	if v, ok := marketplaceVersion(body, "1.95.0"); !ok || v != "2025.2.0" {
		t.Errorf("debe saltar la preliminar y la que exige un VS Code más nuevo: %q %v", v, ok)
	}
	if v, ok := marketplaceVersion(body, "1.100.0"); !ok || v != "2025.4.0" {
		t.Errorf("con VS Code reciente debe elegir la 2025.4.0: %q %v", v, ok)
	}
	if _, ok := marketplaceVersion([]byte(`{"results":[]}`), ""); ok {
		t.Error("respuesta vacía")
	}
}

func TestParseNpmAndChoco(t *testing.T) {
	npm := "npm warn config something\n{\n \"typescript\": {\"current\":\"5.3.3\",\"wanted\":\"5.3.3\",\"latest\":\"5.6.3\"},\n \"same\": {\"current\":\"1.0.0\",\"latest\":\"1.0.0\"},\n \"@angular/cli\": {\"current\":\"17.0.0\",\"latest\":\"18.2.1\"},\n \"bad name!\": {\"current\":\"1\",\"latest\":\"2\"}\n}"
	rows := parseNpmOutdated(npm)
	if len(rows) != 2 || rows[0].Name != "@angular/cli" || rows[1].Name != "typescript" {
		t.Errorf("npm: %+v", rows)
	}
	ch := parseChocoOutdated("Chocolatey v2.2.2\ngit|2.43.0|2.44.0|false\nnodejs|20.0.0|22.0.0|true\nfoo|1.0|\n")
	if len(ch) != 1 || ch[0].Name != "git" || ch[0].Latest != "2.44.0" {
		t.Errorf("choco (los fijados no cuentan): %+v", ch)
	}
}

func TestGitHelpers(t *testing.T) {
	if a, b, ok := parseAheadBehind("1\t3\n"); !ok || a != 1 || b != 3 {
		t.Errorf("parseAheadBehind: %d %d %v", a, b, ok)
	}
	if _, _, ok := parseAheadBehind("fatal: no upstream"); ok {
		t.Error("salida de error")
	}
	if got := cleanRemoteURL("https://user:secret@github.com/a/b.git\n"); strings.Contains(got, "secret") || strings.Contains(got, "user") {
		t.Errorf("no debe mostrar credenciales: %q", got)
	}
}

func TestFindReposSkipsNoise(t *testing.T) {
	root := t.TempDir()
	mk := func(p string) { _ = os.MkdirAll(filepath.Join(root, p), 0o755) }
	mk("a/.git")
	mk("a/sub/.git") // inside a repository: not counted
	mk("work/b/.git")
	mk("node_modules/x/.git")
	mk(".hidden/y/.git")
	mk("deep/1/2/3/4/5/.git")
	repos := findRepos([]string{root}, 4, 100, time.Now().Add(5*time.Second))
	if len(repos) != 2 {
		t.Fatalf("se esperaban 2 repos, hay %d: %v", len(repos), repos)
	}
}

func TestParseGithubRepoAndVersions(t *testing.T) {
	good := map[string]string{
		"owner/repo":                             "owner/repo",
		"https://github.com/owner/repo":          "owner/repo",
		"https://github.com/owner/repo.git":      "owner/repo",
		"https://github.com/owner/repo/releases": "owner/repo",
		"git@github.com:owner/my.tool.git":       "owner/my.tool",
		"  https://www.github.com/Owner/Repo/  ": "Owner/Repo",
	}
	for in, want := range good {
		if got, ok := parseGithubRepo(in); !ok || got != want {
			t.Errorf("parseGithubRepo(%q) = %q %v, quería %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "solo", "a/b/c", "https://example.com/owner/repo", "https://github.com/sponsors/x", "own er/repo", "../etc"} {
		if got, ok := parseGithubRepo(in); ok {
			t.Errorf("parseGithubRepo(%q) debería rechazarse, dio %q", in, got)
		}
	}
	if tagVersion("v1.2.3") != "1.2.3" || tagVersion("release-2.0") != "2.0" || tagVersion("nightly") != "" {
		t.Error("tagVersion")
	}
	if sameScheme("1.2.3", "20260101") || !sameScheme("1.2.3", "2.0") {
		t.Error("sameScheme")
	}
}

func TestPickAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "tool-1.2.3-linux-amd64.tar.gz"}, {Name: "tool-1.2.3-windows-arm64.msi"}, {Name: "checksums.txt"},
		{Name: "tool-1.2.3-windows-x64.zip"}, {Name: "tool-1.2.3-windows-x64.msi"}, {Name: "tool-1.2.3-macos.dmg"},
	}
	as, kind := pickAsset(assets, "")
	if as == nil || as.Name != "tool-1.2.3-windows-x64.msi" || kind != "msi" {
		t.Errorf("debe preferir el MSI de 64 bits: %+v %s", as, kind)
	}
	as, kind = pickAsset(assets, `\.zip$`)
	if as == nil || kind != "zip" {
		t.Errorf("con patrón: %+v %s", as, kind)
	}
	if as, _ := pickAsset([]ghAsset{{Name: "src.tar.gz"}, {Name: "tool.AppImage"}}, ""); as != nil {
		t.Error("sin archivos para Windows no debe elegir ninguno")
	}
	if s, k := scoreAsset("Setup-x64.exe"); s == 0 || k != "setup" {
		t.Errorf("Setup-x64.exe: %d %s", s, k)
	}
}

func TestMissingFrom(t *testing.T) {
	apps := []rawApp{
		{Name: "Microsoft Visual C++ 2015-2022 Redistributable (x64) - 14.38.33135"},
		{Name: "Microsoft Windows Desktop Runtime - 8.0.10 (x64)"},
	}
	ids := map[string]bool{}
	for _, c := range missingFrom(apps, "64 bits") {
		ids[c.ID] = true
	}
	if ids["Microsoft.VCRedist.2015+.x64"] || ids["Microsoft.DotNet.DesktopRuntime.8"] {
		t.Errorf("ya instalados: no deben ofrecerse: %v", ids)
	}
	if !ids["Microsoft.VCRedist.2015+.x86"] || !ids["Microsoft.EdgeWebView2Runtime"] {
		t.Errorf("faltan y deben ofrecerse: %v", ids)
	}
	for _, c := range missingFrom(nil, "32 bits") {
		if c.X64Only {
			t.Errorf("en Windows de 32 bits no se ofrece %s", c.ID)
		}
	}
	for _, c := range missingComponents {
		if !wingetIDRe.MatchString(c.ID) {
			t.Errorf("identificador de winget no válido: %s", c.ID)
		}
	}
}

func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/tool/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.0", "assets": []map[string]any{
				{"name": "tool-1.2.0-windows-x64.msi", "size": 5_000_000, "browser_download_url": "https://github.com/owner/tool/releases/download/v1.2.0/tool-1.2.0-windows-x64.msi"}}})
		case "/repos/other/app/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "2.0.0", "assets": []map[string]any{
				{"name": "app-2.0.0-win64.zip", "size": 1_000_000, "browser_download_url": "https://github.com/other/app/releases/download/2.0.0/app-2.0.0-win64.zip"}}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestScanGithubTrackedAndDiscovered(t *testing.T) {
	srv := fakeGitHub(t)
	defer srv.Close()
	oldBase, oldFile := ghAPIBase, ghFileOver
	ghAPIBase, ghFileOver = srv.URL, filepath.Join(t.TempDir(), "gh.json")
	defer func() { ghAPIBase, ghFileOver = oldBase, oldFile }()

	// add: version 1.0.0 already installed
	if _, err := ghAdd(context.Background(), "https://github.com/owner/tool", false); err != nil {
		t.Fatal(err)
	}
	setGHTag("owner/tool", "v1.0.0")
	if _, err := ghAdd(context.Background(), "nadie/noexiste", false); err == nil {
		t.Error("un repositorio sin releases debe dar error")
	} else if !strings.Contains(errText(err), langSep) {
		t.Errorf("el error debe ser bilingüe: %q", errText(err))
	}
	a := newApp(false)
	apps := []rawApp{
		{Name: "App", Version: "1.5.0", Urls: "https://github.com/other/app/issues"},
		{Name: "Viejo", Version: "20240101", Urls: "https://github.com/other/app"}, // same repo: the duplicate is discarded
		{Name: "Sin web", Version: "1.0"},
	}
	n := a.scanGithub(context.Background(), apps)
	if n != 2 {
		t.Fatalf("se esperaban 2 propuestas (vigilado + descubierto), hay %d: %+v", n, a.items)
	}
	byID := map[string]*Item{}
	for _, it := range a.items {
		byID[it.ID] = it
	}
	tr := byID["github:owner/tool"]
	if tr == nil || !tr.Selectable || tr.From != "v1.0.0" || tr.To != "v1.2.0" || tr.data["kind"] != "msi" {
		t.Errorf("vigilado: %+v", tr)
	}
	ds := byID["github:other/app"]
	if ds == nil || ds.data["origin"] != "discovered" || ds.data["kind"] != "zip" {
		t.Errorf("descubierto: %+v", ds)
	}
	// already on the latest version: not proposed
	setGHTag("owner/tool", "v1.2.0")
	b := newApp(false)
	if got := b.scanGithub(context.Background(), nil); got != 0 {
		t.Errorf("al día: no debe proponer nada (%d)", got)
	}
	if err := ghRemove("owner/tool"); err != nil || len(loadGH()) != 0 {
		t.Errorf("quitar de la lista: %v %v", err, loadGH())
	}
}

func TestInstallGithubRejectsBadInput(t *testing.T) {
	a := newApp(false)
	mk := func(url, name string) *Item {
		return &Item{data: map[string]string{"repo": "o/r", "tag": "v1", "kind": "msi", "url": url, "name": name}}
	}
	for _, it := range []*Item{
		mk("http://github.com/o/r/a.msi", "a.msi"),       // no HTTPS
		mk("https://evil.example.com/a.msi", "a.msi"),    // disallowed domain
		mk("https://github.com.evil.com/a.msi", "a.msi"), // look-alike domain
		mk("https://github.com/o/r/a.msi", "a:b|c.msi"),  // file name with dangerous characters
	} {
		if ok, msg, _ := a.installGithub(context.Background(), it); ok || msg == "" {
			t.Errorf("debía rechazarse %q / %q", it.data["url"], it.data["name"])
		}
	}
}

func TestReposEndToEnd(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git no está instalado")
	}
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command(git, append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	home := t.TempDir()
	origin := filepath.Join(home, "origin")
	_ = os.MkdirAll(origin, 0o755)
	run(origin, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(origin, "a.txt"), []byte("1"), 0o644)
	run(origin, "add", ".")
	run(origin, "commit", "-q", "-m", "uno")
	clone := filepath.Join(home, "proyectos", "clon")
	_ = os.MkdirAll(filepath.Dir(clone), 0o755)
	run(home, "clone", "-q", origin, clone)
	_ = os.WriteFile(filepath.Join(origin, "b.txt"), []byte("2"), 0o644)
	run(origin, "add", ".")
	run(origin, "commit", "-q", "-m", "dos")

	t.Setenv("USERPROFILE", filepath.Join(home, "proyectos")) // the scan starts from here
	a := newApp(false)
	if n := a.scanRepos(context.Background()); n != 1 {
		t.Fatalf("se esperaba 1 repositorio atrasado, hay %d (%+v)", n, a.items)
	}
	it := a.items[0]
	if it.Kind != "repo" || !it.Selectable || it.Group != grpRepos {
		t.Fatalf("elemento: %+v", it)
	}
	ok, msg, _ := a.installRepo(context.Background(), it)
	if !ok {
		t.Fatalf("git pull falló: %s", msg)
	}
	if _, err := os.Stat(filepath.Join(clone, "b.txt")); err != nil {
		t.Errorf("tras actualizar debe existir b.txt: %v", err)
	}
	if n := newApp(false).scanRepos(context.Background()); n != 0 {
		t.Errorf("ya al día: no debe proponer nada (%d)", n)
	}
	// with its own unpushed commits: warning, left alone
	_ = os.WriteFile(filepath.Join(origin, "c.txt"), []byte("3"), 0o644)
	run(origin, "add", ".")
	run(origin, "commit", "-q", "-m", "tres")
	_ = os.WriteFile(filepath.Join(clone, "local.txt"), []byte("x"), 0o644)
	run(clone, "add", ".")
	run(clone, "commit", "-q", "-m", "local")
	c := newApp(false)
	if n := c.scanRepos(context.Background()); n != 1 || c.items[0].Selectable {
		t.Errorf("divergido: debe avisar sin permitir actualizar (%d %+v)", n, c.items)
	}
	_ = fmt.Sprint()
}
