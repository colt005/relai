package render

import (
	"fmt"
	"regexp"
	"strconv"
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

var markerRe = regexp.MustCompile(`\[full L(\d+)–(\d+)\]`)

// linesOf returns lines from..to (1-based, inclusive) of doc.
func linesOf(doc string, from, to int) string {
	lines := strings.Split(doc, "\n")
	return strings.Join(lines[from-1:to], "\n")
}

func TestFullKeepsEverythingAndMarkersPointAtIt(t *testing.T) {
	long := "START " + strings.Repeat("word ", 2000) + "END-OF-LONG-MESSAGE"
	tr := session.Transcript{Msgs: []session.Msg{
		{Role: session.User, Text: "first question"},
		{Role: session.Assistant, Text: long},
		{Role: session.ToolCall, Text: "Bash(ls …[+500 chars])", Full: "{\n  \"command\": \"ls -la /very/long\"\n}"},
		{Role: session.ToolResult, Text: "line one\nline two\nline three"},
		{Role: session.Assistant, Text: "done"},
	}}
	o := Options{AgentLabel: "Test", Snapshot: 5, FullCmd: "relai full test:x --snapshot 5", ToolOutput: true}
	s := session.Session{ID: "x"}
	full := Full(s, tr, o)
	cond := Transcript(s, tr, o)

	if !strings.Contains(full, "END-OF-LONG-MESSAGE") || !strings.Contains(full, "ls -la /very/long") ||
		!strings.Contains(full, "line three") {
		t.Fatalf("full transcript is missing content:\n%s", full)
	}
	if strings.Contains(cond, "END-OF-LONG-MESSAGE") {
		t.Error("condensed transcript should clip long messages")
	}
	body := cond[strings.Index(cond, "## Condensed transcript"):] // the header has an example marker
	markers := markerRe.FindAllStringSubmatch(body, -1)
	if len(markers) != 3 {
		t.Fatalf("want 3 markers (message, tool call, tool result), got %d:\n%s", len(markers), cond)
	}
	// Each marker's lines in the full transcript hold the content it stands for.
	for i, want := range []string{"END-OF-LONG-MESSAGE", "ls -la /very/long", "line three"} {
		from, _ := strconv.Atoi(markers[i][1])
		to, _ := strconv.Atoi(markers[i][2])
		if got := linesOf(full, from, to); !strings.Contains(got, want) {
			t.Errorf("marker %d (L%d–%d) should cover %q, lines are:\n%s", i, from, to, want, got)
		}
	}
	for _, want := range []string{"first 5 records", "relai full test:x --snapshot 5"} {
		if !strings.Contains(cond, want) {
			t.Errorf("handoff header should mention %q", want)
		}
	}
	if !strings.Contains(full, "first 5 records") {
		t.Error("full transcript header should record its snapshot")
	}
}

func TestOmittedMiddleMarkerCoversTheGap(t *testing.T) {
	tr := session.Transcript{Msgs: []session.Msg{{Role: session.User, Text: "THE GOAL"}}}
	for i := 0; i < 100; i++ {
		tr.Msgs = append(tr.Msgs,
			session.Msg{Role: session.User, Text: fmt.Sprintf("middle question %03d", i)},
			session.Msg{Role: session.Assistant, Text: strings.Repeat("filler ", 50)})
	}
	tr.Msgs = append(tr.Msgs, session.Msg{Role: session.Assistant, Text: "LATEST STATE"})
	s := session.Session{ID: "x"}
	o := Options{MaxChars: 3000, AgentLabel: "Test"}
	cond, full := Transcript(s, tr, o), Full(s, tr, o)
	m := markerRe.FindStringSubmatch(cond[strings.Index(cond, "items omitted"):])
	if m == nil {
		t.Fatalf("omitted-items note has no line range:\n%s", cond)
	}
	from, _ := strconv.Atoi(m[1])
	to, _ := strconv.Atoi(m[2])
	gap := linesOf(full, from, to)
	if !strings.Contains(gap, "middle question") || strings.Contains(gap, "THE GOAL") || strings.Contains(gap, "LATEST STATE") {
		t.Errorf("L%d–%d should cover only the omitted middle:\n%s", from, to, gap)
	}
}
