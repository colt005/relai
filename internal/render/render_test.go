package render

import (
	"strings"
	"testing"

	"github.com/colt005/relai/internal/session"
)

func TestBudgetKeepsGoalAndTail(t *testing.T) {
	tr := session.Transcript{Msgs: []session.Msg{{Role: session.User, Text: "THE GOAL"}}}
	for i := 0; i < 200; i++ {
		tr.Msgs = append(tr.Msgs,
			session.Msg{Role: session.User, Text: "middle question"},
			session.Msg{Role: session.Assistant, Text: strings.Repeat("filler ", 50)})
	}
	tr.Msgs = append(tr.Msgs, session.Msg{Role: session.Assistant, Text: "LATEST STATE"})
	out := Transcript(session.Session{ID: "x"}, tr, Options{MaxChars: 5000, AgentLabel: "Test"})
	for _, want := range []string{"THE GOAL", "LATEST STATE", "items omitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if body := out[strings.Index(out, "## Condensed"):]; len(body) > 5500 {
		t.Errorf("body is %d chars, budget 5000", len(body))
	}
}

func TestWithBriefings(t *testing.T) {
	doc := "# H\n\n## Condensed transcript\nbody"
	got := WithBriefings(doc, "## Briefing\nhello", "  ")
	if !strings.Contains(got, "## Briefing\nhello\n\n## Condensed transcript\nbody") {
		t.Errorf("got %q", got)
	}
}
