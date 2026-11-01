// Package launch starts agents, either in the current terminal or in a new
// Terminal.app / iTerm2 window or cmux workspace on macOS.
package launch

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// Quote quotes a single shell argument.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join quotes and joins a command line.
func Join(cmd []string) string {
	q := make([]string, len(cmd))
	for i, c := range cmd {
		q[i] = Quote(c)
	}
	return strings.Join(q, " ")
}

// Run starts cmd in dir. In the current terminal it replaces the relai process,
// so the agent gets the TTY directly. With newWindow it opens a new window
// (macOS only) and returns.
func Run(cmd []string, dir string, newWindow bool) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir, _ = os.Getwd()
	}
	if newWindow {
		// The new window runs the user's login shell, which resolves the agent
		// from their own PATH. relai's PATH may be thinner (e.g. when called from
		// a GUI app), so don't insist on finding it here.
		return newTerminalWindow(cmd, dir)
	}
	bin, err := exec.LookPath(cmd[0])
	if err != nil {
		return fmt.Errorf("%q is not on your PATH", cmd[0])
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	return syscall.Exec(bin, cmd, os.Environ())
}

// CmuxBin finds the cmux CLI: on PATH, or inside the app bundle (a GUI app's PATH
// usually doesn't include it).
func CmuxBin() (string, error) {
	if p, err := exec.LookPath("cmux"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", home + "/Applications"} {
		p := dir + "/cmux.app/Contents/Resources/bin/cmux"
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("cmux isn't installed (couldn't find the cmux command)")
}

// openInCmux starts cmd in a new cmux workspace. cmux only accepts commands from
// outside itself when Settings > Automation allows it.
func openInCmux(cmd []string, dir string) error {
	bin, err := CmuxBin()
	if err != nil {
		return err
	}
	line := Join(cmd)
	out, err := exec.Command(bin, "new-workspace", "--name", "relai", "--cwd", dir,
		"--command", line, "--focus", "true").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Access denied") {
			return fmt.Errorf("cmux refused the request. In cmux, open Settings > Automation and allow " +
				"external access (socket control mode \"automation\"), then try again")
		}
		return fmt.Errorf("cmux: %v: %s", err, msg)
	}
	fmt.Fprintf(os.Stderr, "opened in cmux: cd %s && %s\n", Quote(dir), line)
	return nil
}

func newTerminalWindow(cmd []string, dir string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("--new-window is only supported on macOS")
	}
	line := "cd " + Quote(dir) + " && " + Join(cmd)
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(line)
	app := os.Getenv("RELAI_TERMINAL")
	var script string
	switch strings.ToLower(app) {
	case "cmux":
		return openInCmux(cmd, dir)
	case "iterm", "iterm2":
		script = `tell application "iTerm"
activate
set w to (create window with default profile)
tell current session of w to write text "` + esc + `"
end tell`
	default:
		app = "Terminal"
		script = `tell application "Terminal"
activate
do script "` + esc + `"
end tell`
	}
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %v: %s", err, out)
	}
	fmt.Fprintf(os.Stderr, "opened in %s: %s\n", app, line)
	return nil
}
