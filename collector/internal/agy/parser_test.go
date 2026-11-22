package agy

import (
	"strings"
	"testing"

	"agentbar/collector/internal/schema"
)

// realUsage is a screen dump of `agy /usage` with the 60x140 PTY size.
const realUsage = `└ Models & Quota

  Account: mauriciowolff@gmail.com

GEMINI MODELS
  Models within this group: Gemini 4 Neon, Gemini 4 Hydrogen, Orchestrator (Neon), Gemini 3.8 Flash

  Weekly Limit Remaining
    [█████████████                    ] 39.53%
    Refreshes in 144h 24m

  Five Hour Limit Remaining
    [███████████████████████████████  ] 97.81%
    Refreshes in 2h 34m

CLAUDE AND GPT MODELS
  Models within this group: Claude Opus 5.5, Claude Sonnet 5.5, GPT-OSS 120B

  Weekly Limit Remaining
    [███████████████                  ] 45.94%
    Refreshes in 160h 58m

  Five Hour Limit Remaining
    [████████████████████████████████ ] 100.00%
    Quota available

GEMINI 4 ARGON
  For paid Pro plans, Argon quota is separate. Higher Argon quota (included as part of the larger Gemini quota bucket) is available on Ultra plans.

  Gemini 4 Argon Weekly Limit
    [████████████████████████████████ ] 100.00%
`

func TestParseRealUsage(t *testing.T) {
	p := Parse(realUsage)

	if p.ID != "antigravity" || p.Name != "Antigravity" {
		t.Fatalf("identity: %+v", p)
	}
	if p.Status != schema.StatusOK {
		t.Fatalf("status = %q, want ok (error %q)", p.Status, p.Error)
	}
	if p.Account != "mauriciowolff@gmail.com" {
		t.Fatalf("account = %q", p.Account)
	}
	if len(p.Groups) != 3 {
		t.Fatalf("groups = %d, want 3: %+v", len(p.Groups), p.Groups)
	}

	wantNames := []string{"Gemini Models", "Claude & GPT", "Gemini 4 Argon"}
	for i, want := range wantNames {
		if p.Groups[i].Name != want {
			t.Errorf("group %d name = %q, want %q", i, p.Groups[i].Name, want)
		}
	}

	gem := p.Groups[0]
	if !strings.Contains(gem.Description, "Gemini 4 Neon") {
		t.Errorf("gemini description = %q", gem.Description)
	}
	if len(gem.Windows) != 2 {
		t.Fatalf("gemini windows = %d", len(gem.Windows))
	}

	weekly, five := gem.Windows[0], gem.Windows[1]
	if weekly.ID != "weekly" || weekly.Label != "Weekly" || weekly.RawLabel != "Weekly Limit Remaining" {
		t.Errorf("weekly identity: %+v", weekly)
	}
	if weekly.RemainingPct != 39.53 {
		t.Errorf("weekly remaining = %v, want 39.53", weekly.RemainingPct)
	}
	if weekly.WindowMinutes != schema.WeeklyWindowMinutes {
		t.Errorf("weekly windowMinutes = %d", weekly.WindowMinutes)
	}
	if weekly.ResetsInMinutes == nil || *weekly.ResetsInMinutes != 144*60+24 {
		t.Errorf("weekly resets = %v, want 8664", weekly.ResetsInMinutes)
	}

	if five.ID != "five_hour" || five.Label != "5h" || five.RawLabel != "Five Hour Limit Remaining" {
		t.Errorf("5h identity: %+v", five)
	}
	if five.RemainingPct != 97.81 {
		t.Errorf("5h remaining = %v", five.RemainingPct)
	}
	if five.WindowMinutes != schema.FiveHourWindowMinutes {
		t.Errorf("5h windowMinutes = %d", five.WindowMinutes)
	}
	if five.ResetsInMinutes == nil || *five.ResetsInMinutes != 154 {
		t.Errorf("5h resets = %v, want 154", five.ResetsInMinutes)
	}

	claudeWeekly := p.Groups[1].Windows[0]
	if claudeWeekly.RemainingPct != 45.94 {
		t.Errorf("claude weekly = %v", claudeWeekly.RemainingPct)
	}
	claudeFive := p.Groups[1].Windows[1]
	if claudeFive.RemainingPct != 100 {
		t.Errorf("claude 5h = %v", claudeFive.RemainingPct)
	}
	if claudeFive.ResetsInMinutes != nil {
		t.Errorf("quota available should have nil resets, got %v", *claudeFive.ResetsInMinutes)
	}
	if claudeFive.StatusText != "Quota available" {
		t.Errorf("statusText = %q", claudeFive.StatusText)
	}

	argon := p.Groups[2]
	if len(argon.Windows) != 1 {
		t.Fatalf("argon windows = %d", len(argon.Windows))
	}
	if argon.Windows[0].ID != "weekly" || argon.Windows[0].RemainingPct != 100 {
		t.Errorf("argon window: %+v", argon.Windows[0])
	}
	if !strings.Contains(argon.Description, "Ultra plans") {
		t.Errorf("argon description = %q", argon.Description)
	}
}

func TestParseStripsANSI(t *testing.T) {
	// Escape codes, colors, and cursor positioning as emitted by a real PTY.
	var b strings.Builder
	for i, line := range strings.Split(realUsage, "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString("\x1b[" + itoa(i+1) + ";1H")
			b.WriteString("\x1b[38;5;250m")
		}
		b.WriteString(line)
		b.WriteString("\x1b[0m\x1b[K\r\n")
	}
	p := Parse(b.String())

	if p.Status != schema.StatusOK {
		t.Fatalf("status = %q (%s)", p.Status, p.Error)
	}
	if len(p.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(p.Groups))
	}
	if p.Groups[0].Windows[0].RemainingPct != 39.53 {
		t.Fatalf("weekly = %v", p.Groups[0].Windows[0].RemainingPct)
	}
}

func TestStripANSI(t *testing.T) {
	in := "\x1b[2J\x1b[1;1HHello\x1b[0m \x1b]8;;http://x\x07link\x1b]8;;\x07\r\nWorld\rDone"
	out := StripANSI(in)
	if strings.Contains(out, "\x1b") {
		t.Fatalf("escape left: %q", out)
	}
	for _, want := range []string{"Hello", "link", "World", "Done"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestParseNotSignedIn(t *testing.T) {
	p := Parse("\x1b[1;1HWelcome to Antigravity\r\n\r\nYou are not signed in.\r\nRun `agy` to sign in.\r\n")
	if p.Status != schema.StatusNotSignedIn {
		t.Fatalf("status = %q", p.Status)
	}
	if p.ID != "antigravity" || p.Name != "Antigravity" {
		t.Fatalf("identity: %+v", p)
	}
	if len(p.Groups) != 0 {
		t.Fatalf("groups = %d", len(p.Groups))
	}
}

func TestParseGarbage(t *testing.T) {
	p := Parse("\x1b[1;1Hsome unrelated screen\r\nnothing here\r\n")
	if p.Status != schema.StatusError {
		t.Fatalf("status = %q, want error", p.Status)
	}
	if p.Error == "" {
		t.Fatal("expected error text")
	}
}

func TestParseDurationMinutes(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"144h 24m", 144*60 + 24, true},
		{"2h 34m", 154, true},
		{"45m", 45, true},
		{"6d 0h", 6 * 24 * 60, true},
		{"6d 2h 5m", 6*24*60 + 125, true},
		{"soon", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got := parseDurationMinutes(c.in)
		if c.ok != (got != nil) {
			t.Errorf("%q: nil = %v, want ok=%v", c.in, got == nil, c.ok)
			continue
		}
		if c.ok && *got != c.want {
			t.Errorf("%q = %d, want %d", c.in, *got, c.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"GEMINI MODELS":         "Gemini Models",
		"CLAUDE AND GPT MODELS": "Claude & GPT",
		"GEMINI 4 ARGON":        "Gemini 4 Argon",
	}
	for in, want := range cases {
		if got := displayName(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
