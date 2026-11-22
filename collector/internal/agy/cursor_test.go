package agy

import (
	"strings"
	"testing"

	"agentbar/collector/internal/schema"
)

// agy repaints with cursor moves, splicing footer text onto the group header
// line. The parser must treat movement as a line break, not merge the lines.
func TestParseCursorSplice(t *testing.T) {
	raw := "↑/↓ Scroll · esc Close\r\n" +
		"\x1b[41C\x1b[38;5;110mGemini 4 Neon · medium\r\x1b[m" +
		"\x1b[3A  ⣯ \r\x1b[1mGEMINI MODELS\x1b[m\x1b[K\r\n" +
		"  Models within this group: Gemini 4 Neon\r\n" +
		"\r\n  Weekly Limit Remaining\r\n" +
		"\x1b[38;5;42m[██████░░░░░░░░░░░░░░░░░░░░░░░░░░░] 39.38%\x1b[m\r\n" +
		"Refreshes in 144h 16m\r\n"

	p := Parse(raw)
	if p.Status != schema.StatusOK || len(p.Groups) != 1 {
		t.Fatalf("groups = %d status %q (%s)", len(p.Groups), p.Status, p.Error)
	}
	if p.Groups[0].Name != "Gemini Models" {
		t.Errorf("name = %q", p.Groups[0].Name)
	}
	if !strings.Contains(p.Groups[0].Description, "Gemini 4 Neon") {
		t.Errorf("description = %q", p.Groups[0].Description)
	}
	if len(p.Groups[0].Windows) != 1 || p.Groups[0].Windows[0].RemainingPct != 39.38 {
		t.Errorf("windows = %+v", p.Groups[0].Windows)
	}
}

func TestParseSplashThenSuccess(t *testing.T) {
	raw := "Welcome to the Antigravity CLI. You are currently not signed in.\r\n\r\n ⣷  Signing in...\r\n" + realUsage
	p := Parse(raw)
	if p.Status != schema.StatusOK {
		t.Fatalf("status = %q, want ok", p.Status)
	}
	if len(p.Groups) != 3 {
		t.Fatalf("groups = %d", len(p.Groups))
	}
}
