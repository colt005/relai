// Package agents is the registry of supported agents.
package agents

import (
	"sort"

	"github.com/colt005/relai/internal/agents/claude"
	"github.com/colt005/relai/internal/agents/codex"
	"github.com/colt005/relai/internal/agents/cursor"
	"github.com/colt005/relai/internal/agents/gemini"
	"github.com/colt005/relai/internal/session"
)

// All returns every registered adapter keyed by name.
func All() map[string]session.Adapter {
	return map[string]session.Adapter{
		"claude": claude.New(),
		"codex":  codex.New(),
		"cursor": cursor.New(),
		"gemini": gemini.New(),
	}
}

// Names returns registered agent names, sorted.
func Names() []string {
	var n []string
	for k := range All() {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}
