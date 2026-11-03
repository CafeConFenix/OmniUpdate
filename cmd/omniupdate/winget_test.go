package main

import "testing"

const wingetEN = "\r-\r\\\r|\r/\r-\r   \r  \r" +
	"Name                          Id                          Version   Available Source\r\n" +
	"----------------------------------------------------------------------------------------\r\n" +
	"Microsoft Visual Studio Code  Microsoft.VisualStudioCode  1.84.2    1.85.1    winget\r\n" +
	"7-Zip 23.01 (x64)             7zip.7zip                   23.01     24.07     winget\r\n" +
	"Microsoft Teams               XP8BT8DW290MPQ              < 1.5     1.7.0     msstore\r\n" +
	"Foo Bar Application with a lo… Vendor.FooBar               Unknown   3.0.0     winget\r\n" +
	"4 upgrades available.\r\n"

const wingetES = "   - \r   \\ \r   | \r" +
	"Nombre                       Id                         Versión   Disponible  Origen\n" +
	"-----------------------------------------------------------------------------------\n" +
	"Google Chrome                Google.Chrome              118.0.1   119.0.2     winget\n" +
	"Cliente de Teams (clásico)   Microsoft.Teams.Classic    1.6.0.1   1.6.0.9     winget\n" +
	"Instalador de Python 3.12    Python.Python.3.12         3.12.0    3.12.1      winget\n" +
	"Ñandú Reproductor ★ 日本語    Nandu.Player               Desconocido 2.0     winget\n" +
	"3 actualizaciones disponibles.\n" +
	"Las siguientes actualizaciones tienen versiones desconocidas y requieren que se indiquen explícitamente.\n" +
	"\n" +
	"Nombre        Id          Versión  Disponible  Origen\n" +
	"-----------------------------------------------------\n" +
	"Notepad++     Notepad++.Notepad++   8.5.0   8.6.0   winget\n"

func TestParseEnglish(t *testing.T) {
	pk := parseWingetUpgrade(wingetEN)
	if len(pk) != 4 {
		t.Fatalf("esperaba 4 filas, hay %d: %+v", len(pk), pk)
	}
	if pk[0].ID != "Microsoft.VisualStudioCode" || pk[0].Version != "1.84.2" || pk[0].Available != "1.85.1" || pk[0].Source != "winget" {
		t.Errorf("fila 0 mal: %+v", pk[0])
	}
	if pk[0].Name != "Microsoft Visual Studio Code" {
		t.Errorf("nombre mal: %q", pk[0].Name)
	}
	if pk[1].Name != "7-Zip 23.01 (x64)" || pk[1].ID != "7zip.7zip" {
		t.Errorf("fila 1 mal: %+v", pk[1])
	}
	if pk[2].Version != "< 1.5" || pk[2].Source != "msstore" || pk[2].ID != "XP8BT8DW290MPQ" {
		t.Errorf("fila 2 mal: %+v", pk[2])
	}
	if pk[3].ID != "Vendor.FooBar" || pk[3].Version != "Unknown" {
		t.Errorf("fila 3 mal: %+v", pk[3])
	}
}

func TestParseSpanishWithSecondTable(t *testing.T) {
	pk := parseWingetUpgrade(wingetES)
	if len(pk) != 5 {
		t.Fatalf("esperaba 5 filas, hay %d: %+v", len(pk), pk)
	}
	if pk[3].Version != "Desconocido" || pk[3].Available != "2.0" {
		t.Errorf("fila unicode mal: %+v", pk[3])
	}
	if pk[4].ID != "Notepad++.Notepad++" || pk[4].Available != "8.6.0" {
		t.Errorf("segunda tabla mal: %+v", pk[4])
	}
}

func TestParseByColumnsFallback(t *testing.T) {
	pk := parseWingetUpgradeByColumns(wingetEN)
	if len(pk) < 3 {
		t.Fatalf("el método alternativo debería leer al menos 3 filas, leyó %d", len(pk))
	}
	if pk[0].ID != "Microsoft.VisualStudioCode" {
		t.Errorf("mal: %+v", pk[0])
	}
}

func TestParseTruncatedID(t *testing.T) {
	out := "Name  Id  Version Available Source\n----------------------------------------\n" +
		"Long thing  Some.Very.Long.Identifier.Tha…  1.0  2.0  winget\n"
	pk := parseWingetUpgrade(out)
	if len(pk) != 1 || !pk[0].Truncated {
		t.Fatalf("debería marcar ID truncado: %+v", pk)
	}
}

func TestParseNothing(t *testing.T) {
	if pk := parseWingetUpgrade("No installed package found matching input criteria.\n"); len(pk) != 0 {
		t.Fatalf("no debería haber filas: %+v", pk)
	}
	if pk := parseWingetUpgrade("-\r\\\r|\r/\r"); len(pk) != 0 {
		t.Fatalf("el ruido no son filas: %+v", pk)
	}
}

// Real output of «winget upgrade» in Spanish (captured from a user): the summary line is stuck to the table.
const wingetReal = "Nombre                 Id                       Versión                  Disponible               Origen\n" +
	"----------------------------------------------------------------------------------------------------------\n" +
	"Cloudflare One Client  Cloudflare.Warp          26.8.2110.0              26.8.2115.0              winget\n" +
	"Google Chrome          Google.Chrome            156.0.8078.47            156.0.8078.48            winget\n" +
	"Ollama version 0.40.5  Ollama.Ollama            0.40.5                   0.40.10                  winget\n" +
	"Spotify                Spotify.Spotify          1.3.5.3058.g5d40d5480152 1.3.6.1921.g7086c071a617 winget\n" +
	"Tailscale              Tailscale.Tailscale      1.104.1                  1.104.2                  winget\n" +
	"5 actualizaciones disponibles.\n"

func TestParseRealUserOutput(t *testing.T) {
	pk := parseWingetUpgrade(wingetReal)
	if len(pk) != 5 {
		t.Fatalf("esperaba las 5 de la captura, hay %d: %+v", len(pk), pk)
	}
	want := []string{"Cloudflare.Warp", "Google.Chrome", "Ollama.Ollama", "Spotify.Spotify", "Tailscale.Tailscale"}
	for i, w := range want {
		if pk[i].ID != w || pk[i].Truncated || pk[i].Source != "winget" {
			t.Errorf("fila %d mal: %+v", i, pk[i])
		}
	}
	if pk[2].Name != "Ollama version 0.40.5" || pk[2].Version != "0.40.5" || pk[2].Available != "0.40.10" {
		t.Errorf("Ollama mal: %+v", pk[2])
	}
	if pk[3].Available != "1.3.6.1921.g7086c071a617" {
		t.Errorf("Spotify mal: %+v", pk[3])
	}
	if n := wingetSummaryCount(wingetReal); n != 5 {
		t.Errorf("recuento de la línea resumen: %d", n)
	}
	if n := wingetSummaryCount(wingetEN); n != 4 {
		t.Errorf("recuento en inglés: %d", n)
	}
	if n := wingetSummaryCount("No installed package found matching input criteria.\n"); n != 0 {
		t.Errorf("sin tabla debe dar 0: %d", n)
	}
}

func TestParseWingetModule(t *testing.T) {
	out := `{"Ok":true,"Items":[{"Name":"Google Chrome","Id":"Google.Chrome","Version":"156.0.8078.47","Available":"156.0.8078.48","Source":"winget"},
	{"Name":"Raro","Id":"no valido!","Version":"1","Available":"2","Source":"winget"}]}`
	pk, err := parseWingetModule(out)
	if err != nil || len(pk) != 1 || pk[0].ID != "Google.Chrome" || pk[0].Source != "winget" {
		t.Fatalf("mal: %v %+v", err, pk)
	}
	one := `{"Ok":true,"Items":{"Name":"Tailscale","Id":"Tailscale.Tailscale","Version":"1.104.1","Available":"1.104.2","Source":"winget"}}`
	if pk, err := parseWingetModule(one); err != nil || len(pk) != 1 {
		t.Errorf("un solo elemento (objeto) mal: %v %+v", err, pk)
	}
	if pk, err := parseWingetModule(`{"Ok":true,"Items":[]}`); err != nil || len(pk) != 0 {
		t.Errorf("lista vacía mal: %v %+v", err, pk)
	}
	if _, err := parseWingetModule(`{"Ok":false,"Error":"sin red"}`); err == nil {
		t.Errorf("un error del módulo debe devolver error")
	}
}
