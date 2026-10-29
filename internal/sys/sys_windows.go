//go:build windows

package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procIsUserAnAdmin = shell32.NewProc("IsUserAnAdmin")
	procShellExecute  = shell32.NewProc("ShellExecuteW")
	procMessageBox    = user32.NewProc("MessageBoxW")
	procCreateMutex   = kernel32.NewProc("CreateMutexW")
	procCloseHandle   = kernel32.NewProc("CloseHandle")

	procGetLogicalDrives = kernel32.NewProc("GetLogicalDrives")
	procGetDriveType     = kernel32.NewProc("GetDriveTypeW")
	procGetUILanguage    = kernel32.NewProc("GetUserDefaultUILanguage")
)

const (
	createNoWindow      = 0x08000000
	detachedProcess     = 0x00000008
	createNewProcessGrp = 0x00000200
	errorAlreadyExists  = 183
	swShowNormal        = 1
)

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// IsAdmin reports whether the process is running with administrator (elevated) rights.
func IsAdmin() bool {
	r, _, _ := procIsUserAnAdmin.Call()
	return r != 0
}

// RelaunchElevated relaunches this same executable asking for administrator
// rights (the Windows UAC prompt appears).
func RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	params := ""
	for i, a := range args {
		if i > 0 {
			params += " "
		}
		params += syscall.EscapeArg(a)
	}
	r, _, e := procShellExecute.Call(0,
		uintptr(unsafe.Pointer(utf16("runas"))),
		uintptr(unsafe.Pointer(utf16(exe))),
		uintptr(unsafe.Pointer(utf16(params))),
		uintptr(unsafe.Pointer(utf16(filepath.Dir(exe)))),
		swShowNormal)
	if r <= 32 {
		return fmt.Errorf("no se pudo pedir permisos de administrador (código %d: %v)", r, e)
	}
	return nil
}

// MessageBox shows a native dialog box and returns the button pressed.
func MessageBox(title, text string, flags uint32) int {
	r, _, _ := procMessageBox.Call(0,
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(unsafe.Pointer(utf16(title))),
		uintptr(flags|0x00040000)) // MB_TOPMOST
	return int(r)
}

// SingleInstance tries to acquire a named mutex. ok=false if another copy is already running.
func SingleInstance(name string) (release func(), ok bool) {
	h, _, e := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(utf16(name))))
	if h == 0 {
		return func() {}, true // could not check: do not block
	}
	if errno, isErr := e.(syscall.Errno); isErr && errno == errorAlreadyExists {
		procCloseHandle.Call(h)
		return func() {}, false
	}
	return func() { procCloseHandle.Call(h) }, true
}

// Command creates a command without a console window.
func Command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}

// CommandVisible creates a command that may show its own window (installers with a wizard).
func CommandVisible(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// Shell runs a command line with cmd.exe (resolves aliases such as winget).
func Shell(line string) *exec.Cmd {
	c := exec.Command("cmd.exe")
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       `cmd.exe /d /s /c "` + line + `"`,
	}
	return c
}

// StartDetached launches a cmd.exe command line that keeps running after this process exits.
func StartDetached(line string) error {
	c := exec.Command("cmd.exe")
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNewProcessGrp,
		CmdLine:       `cmd.exe /d /s /c "` + line + `"`,
	}
	return c.Start()
}

// OpenURL opens a web address or folder with the default program.
func OpenURL(url string) error {
	return Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

// FindBrowser returns the path of Edge or Chrome (to open the app window).
func FindBrowser() string {
	var dirs []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		if v := os.Getenv(env); v != "" {
			dirs = append(dirs, v)
		}
	}
	rel := []string{
		`Microsoft\Edge\Application\msedge.exe`,
		`Google\Chrome\Application\chrome.exe`,
	}
	for _, r := range rel {
		for _, d := range dirs {
			p := filepath.Join(d, r)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

// FixedDrives returns the fixed disk drives ("C:\\", "D:\\"...); it ignores network, CD and USB.
func FixedDrives() []string {
	mask, _, _ := procGetLogicalDrives.Call()
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		if t, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(utf16(root)))); t == 3 { // DRIVE_FIXED
			out = append(out, root)
		}
	}
	return out
}

// UILang returns "es" if the Windows language is Spanish and "en" in any other case.
func UILang() string {
	id, _, _ := procGetUILanguage.Call()
	if id&0x3FF == 0x0A { // LANG_SPANISH
		return "es"
	}
	return "en"
}
