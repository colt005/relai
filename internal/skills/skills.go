// Package skills embeds the relai skill so it ships inside the binary and is
// updated whenever relai is.
package skills

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed SKILL.md
var SkillMD []byte

// Targets returns the skill directories relai installs into.
func Targets() []string {
	home, _ := os.UserHomeDir()
	claude := filepath.Join(home, ".claude", "skills")
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		claude = filepath.Join(d, "skills")
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	return []string{claude, filepath.Join(codexHome, "skills")}
}

// Install writes SKILL.md into each target and returns the files written.
func Install() ([]string, error) {
	var written []string
	for _, t := range Targets() {
		dir := filepath.Join(t, "relai")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, err
		}
		p := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(p, SkillMD, 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	return written, nil
}
