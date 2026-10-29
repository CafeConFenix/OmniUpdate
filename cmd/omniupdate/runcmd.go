package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"omniupdate/internal/sys"
)

// decodeOutput converts a program's output to UTF-8 even if it comes in
// UTF-16 or in a legacy code page.
func decodeOutput(b []byte) string {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	if len(b) >= 2 && ((b[0] == 0xFF && b[1] == 0xFE) || (len(b) > 4 && b[1] == 0 && b[3] == 0)) {
		if b[0] == 0xFF && b[1] == 0xFE {
			b = b[2:]
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	if utf8.Valid(b) {
		return string(b)
	}
	// Last resort: Latin-1 (identifiers and versions are ASCII).
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

type cmdResult struct {
	Out   string
	Exit  int
	Err   error
	Timed bool
}

func runCmd(parent context.Context, timeout time.Duration, c *exec.Cmd) cmdResult {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Start(); err != nil {
		return cmdResult{Exit: -1, Err: err}
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		res := cmdResult{Out: decodeOutput(buf.Bytes()), Exit: 0}
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				res.Exit = ee.ExitCode()
			} else {
				res.Exit, res.Err = -1, err
			}
		}
		return res
	case <-ctx.Done():
		_ = c.Process.Kill()
		<-done
		return cmdResult{Out: decodeOutput(buf.Bytes()), Exit: -1, Timed: true, Err: ctx.Err()}
	}
}

func runShell(ctx context.Context, timeout time.Duration, line string) cmdResult {
	return runCmd(ctx, timeout, sys.Shell(line))
}

var tempDir = func() string {
	d := filepath.Join(os.TempDir(), "OmniUpdate")
	_ = os.MkdirAll(d, 0o755)
	return d
}()

const psPrelude = "$ErrorActionPreference = 'Continue'\r\n$ProgressPreference = 'SilentlyContinue'\r\n" +
	"[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false\r\n"

// runPS runs a PowerShell script (saved to a temporary .ps1).
func runPS(ctx context.Context, timeout time.Duration, script string, args ...string) cmdResult {
	f, err := os.CreateTemp(tempDir, "ps-*.ps1")
	if err != nil {
		return cmdResult{Exit: -1, Err: err}
	}
	path := f.Name()
	defer os.Remove(path)
	_, _ = f.WriteString("\xEF\xBB\xBF" + psPrelude + strings.ReplaceAll(script, "\r\n", "\n"))
	f.Close()
	all := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path}, args...)
	return runCmd(ctx, timeout, sys.Command("powershell.exe", all...))
}

func shortErr(r cmdResult) string {
	if r.Err != nil {
		return r.Err.Error()
	}
	return Tf("código de salida %d", "exit code %d", r.Exit)
}

// lastLine returns the last non-empty (trimmed) line of a text.
func lastLine(s string, max int) string {
	lines := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t != "" && strings.Trim(t, "-\\|/ ") != "" {
			if len([]rune(t)) > max {
				t = string([]rune(t)[:max]) + "…"
			}
			return t
		}
	}
	return ""
}

// runCmdEnv runs a command with extra environment variables.
func runCmdEnv(parent context.Context, timeout time.Duration, env []string, c *exec.Cmd) cmdResult {
	c.Env = append(os.Environ(), env...)
	return runCmd(parent, timeout, c)
}

// readLimited reads at most max bytes.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, max))
}
