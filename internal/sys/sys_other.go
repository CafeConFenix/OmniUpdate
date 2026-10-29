//go:build !windows

package sys

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func IsAdmin() bool                                 { return true }
func RelaunchElevated(args []string) error          { return errors.New("solo disponible en Windows") }
func SingleInstance(string) (func(), bool)          { return func() {}, true }
func FindBrowser() string                           { return "" }
func OpenURL(url string) error                      { fmt.Println("abrir:", url); return nil }
func StartDetached(line string) error               { return exec.Command("sh", "-c", line).Start() }
func Command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
func Shell(line string) *exec.Cmd                   { return exec.Command("sh", "-c", line) }

func MessageBox(title, text string, flags uint32) int {
	fmt.Printf("[%s] %s\n", title, text)
	return IDYes
}

// FixedDrives: on other systems there are no drives to iterate over.
func FixedDrives() []string { return nil }

// UILang takes the language from the LANG variable (only for testing outside Windows).
func UILang() string {
	if strings.HasPrefix(strings.ToLower(os.Getenv("LANG")), "es") {
		return "es"
	}
	return "en"
}

// CommandVisible: same as Command outside Windows.
func CommandVisible(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
