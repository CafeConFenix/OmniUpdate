package main

import (
	"errors"
	"strings"
	"testing"
)

func TestBilingualHelpers(t *testing.T) {
	s := T("Hola", "Hello")
	if pick(s, "es") != "Hola" || pick(s, "en") != "Hello" {
		t.Fatalf("pick: %q", s)
	}
	if pick("plano", "en") != "plano" || pick("plano", "es") != "plano" {
		t.Error("un texto sin marca debe servir para los dos idiomas")
	}
	f := Tf("%d cosa(s) en %s", "%d thing(s) in %s", 3, T("casa", "home"))
	if pick(f, "es") != "3 cosa(s) en casa" || pick(f, "en") != "3 thing(s) in home" {
		t.Errorf("Tf no reparte los argumentos bilingües: %q", f)
	}
	e := terrf("falló %s", "failed %s", "x")
	if pick(errText(e), "en") != "failed x" || pick(errText(e), "es") != "falló x" {
		t.Errorf("terrf: %q", errText(e))
	}
	// a normal error (no marker) inside Tf is shown the same in both languages
	g := Tf("error: %v", "error: %v", errors.New("boom"))
	if pick(g, "es") != "error: boom" || pick(g, "en") != "error: boom" {
		t.Errorf("error plano: %q", g)
	}
	c := Cat(T("a", "A"), "-", T("b", "B"))
	if pick(c, "es") != "a-b" || pick(c, "en") != "A-B" {
		t.Errorf("Cat: %q", c)
	}
	j := Join(" · ", T("uno", "one"), "2", T("tres", "three"))
	if pick(j, "es") != "uno · 2 · tres" || pick(j, "en") != "one · 2 · three" {
		t.Errorf("Join: %q", j)
	}
	if plainES(s) != "Hola" {
		t.Error("plainES")
	}
}

// All user-visible texts must have both versions, and neither may be empty.
func TestGroupsAndStagesAreBilingual(t *testing.T) {
	check := func(what, s string) {
		if !strings.Contains(s, langSep) {
			t.Errorf("%s sin traducción: %q", what, s)
			return
		}
		if strings.TrimSpace(pick(s, "es")) == "" || strings.TrimSpace(pick(s, "en")) == "" {
			t.Errorf("%s con un idioma vacío: %q", what, s)
		}
	}
	ids := map[string]bool{}
	for _, g := range groupDefs {
		check("grupo "+g.ID, g.Title)
		ids[g.ID] = true
	}
	for _, st := range defaultStages() {
		check("paso "+st.ID, st.Label)
	}
	for _, g := range []string{grpAtencion, grpFaltan, grpProgs, grpPlugins, grpGithub, grpRepos, grpWindows, grpDrivers, grpFabr, grpSinDrv} {
		if !ids[g] {
			t.Errorf("grupo %q sin definir en groupDefs", g)
		}
	}
}

func TestSnapshotHasNoNilSlices(t *testing.T) {
	a := newApp(true)
	s := a.snapshot()
	if s.Items == nil || s.Devices == nil || s.Programs == nil || s.Log == nil || s.Stages == nil || s.Groups == nil {
		t.Error("las listas deben ser [] y no null en el JSON (la interfaz se rompe con null)")
	}
}
