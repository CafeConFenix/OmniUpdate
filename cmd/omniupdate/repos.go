package main

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"omniupdate/internal/sys"
)

// Git repositories: finds the repositories cloned on the machine, checks whether their branch
// is behind the remote and updates them with «git pull --ff-only» (never merges or
// rewrites anything: if it is not a clean fast-forward, it is left alone).

var gitNoPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS="}

func gitPath() string {
	if p, err := exec.LookPath("git"); err == nil {
		return p
	}
	return firstExisting(filepath.Join(pf(), `Git\cmd\git.exe`), filepath.Join(pf86(), `Git\cmd\git.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Programs\Git\cmd\git.exe`))
}

// folders that are never traversed when searching for repositories
var skipRepoDirs = map[string]bool{
	"node_modules": true, "appdata": true, "$recycle.bin": true, "windows": true, "program files": true,
	"program files (x86)": true, "programdata": true, "system volume information": true, "venv": true,
	"site-packages": true, "target": true, "__pycache__": true, "vendor": true,
}

func remoteSuffix(r string) string {
	if r == "" {
		return ""
	}
	return " · " + r
}

func skipRepoDir(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, ".") || skipRepoDirs[n]
}

// findRepos walks the given folders (up to maxDepth levels) and returns those that contain a repository.
func findRepos(roots []string, maxDepth, limit int, deadline time.Time) []string {
	seen := map[string]bool{}
	var repos []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if len(repos) >= limit || time.Now().After(deadline) {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
			repos = append(repos, filepath.Clean(dir))
			return // do not search inside a repository
		}
		if depth >= maxDepth {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if e.IsDir() && !skipRepoDir(e.Name()) {
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	sort.Strings(repos)
	return repos
}

// repoRoots are the usual places where repositories are cloned.
func repoRoots() (roots []string, depth int) {
	if p := os.Getenv("USERPROFILE"); p != "" {
		roots = append(roots, p)
	}
	for _, drive := range sys.FixedDrives() {
		for _, n := range []string{"dev", "src", "source", "repos", "repo", "code", "projects", "proyectos", "git", "github", "workspace", "work"} {
			if st, err := os.Stat(filepath.Join(drive, n)); err == nil && st.IsDir() {
				roots = append(roots, filepath.Join(drive, n))
			}
		}
	}
	return roots, 4
}

// parseAheadBehind reads the output of «git rev-list --left-right --count HEAD...@{u}».
func parseAheadBehind(out string) (ahead, behind int, ok bool) {
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, false
	}
	a, e1 := strconv.Atoi(f[0])
	b, e2 := strconv.Atoi(f[1])
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return a, b, true
}

// cleanRemoteURL strips the username and password from a remote's address.
func cleanRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.User = nil
		return u.String()
	}
	return raw
}

type repoStatus struct {
	Path, Branch, Upstream, Head, UpHead, Remote string
	Ahead, Behind                                int
	Dirty                                        bool
}

func gitOut(ctx context.Context, timeout time.Duration, git, path string, args ...string) cmdResult {
	full := append([]string{"-C", path}, args...)
	return runCmdEnv(ctx, timeout, gitNoPrompt, sys.Command(git, full...))
}

// repoInfo queries (and fetches) the state of a repository. ok=false if there is nothing to report.
func (a *App) repoInfo(ctx context.Context, git, path string) (st repoStatus, ok bool) {
	st.Path = path
	up := gitOut(ctx, 30*time.Second, git, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if up.Exit != 0 {
		return st, false // no associated remote branch
	}
	st.Upstream = strings.TrimSpace(up.Out)
	if f := gitOut(ctx, 2*time.Minute, git, path, "fetch", "--quiet", "--prune", "--no-tags"); f.Exit != 0 {
		a.diagf("git fetch %s (código %d): %s", path, f.Exit, truncate(f.Out, 500))
		return st, false
	}
	ab := gitOut(ctx, 30*time.Second, git, path, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	var good bool
	if st.Ahead, st.Behind, good = parseAheadBehind(ab.Out); !good {
		return st, false
	}
	st.Branch = strings.TrimSpace(gitOut(ctx, 20*time.Second, git, path, "rev-parse", "--abbrev-ref", "HEAD").Out)
	st.Head = strings.TrimSpace(gitOut(ctx, 20*time.Second, git, path, "rev-parse", "--short", "HEAD").Out)
	st.UpHead = strings.TrimSpace(gitOut(ctx, 20*time.Second, git, path, "rev-parse", "--short", "@{u}").Out)
	st.Dirty = strings.TrimSpace(gitOut(ctx, 60*time.Second, git, path, "status", "--porcelain", "--untracked-files=no").Out) != ""
	if i := strings.Index(st.Upstream, "/"); i > 0 {
		st.Remote = cleanRemoteURL(gitOut(ctx, 20*time.Second, git, path, "remote", "get-url", st.Upstream[:i]).Out)
	}
	return st, true
}

func (a *App) scanRepos(ctx context.Context) int {
	roots, depth := repoRoots()
	repos := findRepos(roots, depth, 300, time.Now().Add(25*time.Second))
	a.diagf("Repositorios encontrados: %d\n%s", len(repos), strings.Join(repos, "\n"))
	if len(repos) == 0 {
		return 0
	}
	git := gitPath()
	if git == "" {
		a.addItem(&Item{ID: "missing:git", Kind: "winget", Group: grpFaltan, Selectable: true,
			Name:   "Git",
			Detail: Tf("Hay %d repositorio(s) en este equipo y Git no está instalado", "There are %d repositor(ies) on this PC and Git is not installed", len(repos)),
			To:     T("Instalar", "Install"),
			Note:   T("Hace falta para mantener tus repositorios al día.", "Needed to keep your repositories up to date."),
			data:   map[string]string{"id": "Git.Git", "source": "winget", "mode": "install"}})
		return 0
	}
	type res struct {
		st repoStatus
		ok bool
	}
	results := make([]res, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, p := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p string) {
			defer wg.Done()
			defer func() { <-sem }()
			st, ok := a.repoInfo(ctx, git, p)
			results[i] = res{st, ok}
		}(i, p)
	}
	wg.Wait()
	n := 0
	for _, r := range results {
		st := r.st
		if !r.ok || st.Behind == 0 {
			continue
		}
		it := &Item{ID: "repo:" + st.Path, Kind: "repo", Group: grpRepos, Name: filepath.Base(st.Path),
			Detail: st.Path + " · " + st.Branch + " ← " + st.Upstream + remoteSuffix(st.Remote),
			From:   st.Head, To: st.UpHead,
			data: map[string]string{"path": st.Path}}
		notes := []string{Tf("%d commit(s) nuevos", "%d new commit(s)", st.Behind)}
		switch {
		case st.Ahead > 0:
			it.Selectable = false
			notes = append(notes, Tf("Tu rama tiene %d commit(s) propios sin subir: hay que fusionar a mano", "Your branch has %d local commit(s) that are not pushed: merge manually", st.Ahead))
		default:
			it.Selectable = true
			if st.Dirty {
				notes = append(notes, T("Tiene cambios sin guardar; si chocan con la actualización, no se toca nada", "It has uncommitted changes; if they clash with the update, nothing is touched"))
			}
		}
		it.Note = Join(" · ", notes...)
		a.addItem(it)
		n++
	}
	return n
}

func (a *App) installRepo(ctx context.Context, it *Item) (bool, string, bool) {
	path := it.data["path"]
	git := gitPath()
	if git == "" {
		return false, T("Git no está instalado", "Git is not installed"), false
	}
	if st, err := os.Stat(filepath.Join(path, ".git")); err != nil || !st.IsDir() {
		return false, T("La carpeta ya no es un repositorio", "The folder is no longer a repository"), false
	}
	r := gitOut(ctx, 15*time.Minute, git, path, "pull", "--ff-only", "--no-edit")
	a.diagf("=== git pull %s (código %d) ===\n%s", path, r.Exit, truncate(r.Out, 3000))
	switch {
	case r.Timed:
		return false, T("Tardó demasiado y se canceló", "It took too long and was cancelled"), false
	case r.Exit == 0:
		return true, T("Repositorio actualizado", "Repository updated"), false
	}
	return false, Cat(Tf("No se pudo actualizar (código %d): ", "Could not update (code %d): ", r.Exit), lastLine(r.Out, 160)), false
}
