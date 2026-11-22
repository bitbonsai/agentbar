package claude

import (
	"testing"
)

const subscriptionScreen = `Claude Code v2.1.330
Claude Max with high effort · Claude Max plan
/Users/you/project
❯ /usage
Settings  Status   Config   Usage   Stats
Plan usage limits
Current session
███░░░░░░░ 42% used
Resets in 2h 15m
Current week (all models)
█░░░░░░░░░ 12.5% used
Resets Monday at 9:00 AM
←/→ to switch · Esc to close
`

const apiBillingScreen = `Claude Code v2.1.330
Opus 5.5 with medium effort · API Usage Billing
/tmp/scratch
Settings  Status   Config   Usage   Stats
Session
Estimated cost: $0.0000
Total duration (API): 0s
Usage: 0 input, 0 output, 0 cache read, 0 cache write
←/→ to switch · Esc to close
`

func TestParseSubscriptionScreen(t *testing.T) {
	p := Parse(subscriptionScreen)
	if p.ID != "claude" || p.Name != "Claude Code" {
		t.Fatalf("identity = %q/%q", p.ID, p.Name)
	}
	if p.Account != "Claude Max plan" {
		t.Errorf("account = %q, want Claude Max plan", p.Account)
	}
	if len(p.Groups) != 1 || len(p.Groups[0].Windows) != 2 {
		t.Fatalf("groups = %+v", p.Groups)
	}
	session, week := p.Groups[0].Windows[0], p.Groups[0].Windows[1]
	if session.ID != "five_hour" || session.Label != "5h" || session.WindowMinutes != 300 {
		t.Errorf("session window = %+v", session)
	}
	if session.RemainingPct != 58 {
		t.Errorf("session remaining = %v, want 58", session.RemainingPct)
	}
	if session.ResetsInMinutes == nil || *session.ResetsInMinutes != 135 {
		t.Errorf("session resets = %v, want 135", session.ResetsInMinutes)
	}
	if week.ID != "weekly" || week.RemainingPct != 87.5 {
		t.Errorf("week window = %+v", week)
	}
	if week.StatusText != "Resets Monday at 9:00 AM" {
		t.Errorf("week status = %q", week.StatusText)
	}
}

func TestParseStripsANSI(t *testing.T) {
	raw := "\x1b[2J\x1b[1;1HCurrent session\r\n\x1b[32m███ 30% used\x1b[0m\r\nResets in 45m\r\n"
	p := Parse(raw)
	if len(p.Groups) != 1 || len(p.Groups[0].Windows) != 1 {
		t.Fatalf("groups = %+v", p.Groups)
	}
	w := p.Groups[0].Windows[0]
	if w.RemainingPct != 70 || w.ResetsInMinutes == nil || *w.ResetsInMinutes != 45 {
		t.Errorf("window = %+v", w)
	}
}

func TestParseAPIBillingHasNoQuota(t *testing.T) {
	p := Parse(apiBillingScreen)
	if p.Status != "ok" {
		t.Errorf("status = %q, want ok", p.Status)
	}
	if len(p.Groups) != 0 {
		t.Errorf("groups = %+v, want none", p.Groups)
	}
	if p.Account != "API Usage Billing" {
		t.Errorf("account = %q", p.Account)
	}
}

func TestParseSignedOut(t *testing.T) {
	raw := "Please run /login to continue\n"
	if !isSignedOut(raw) {
		t.Fatal("expected signed-out text to be detected")
	}
	if isSignedOut(subscriptionScreen) {
		t.Fatal("subscription screen must not look signed out")
	}
}

func TestSpecForIgnoresSessionMetadata(t *testing.T) {
	for _, line := range []string{
		"Session ID: 598d7a9d",
		"Session name: my session",
		"Sessions: 2.4k",
	} {
		if _, ok := specFor(line); ok {
			t.Errorf("%q must not be a quota label", line)
		}
	}
	for line, want := range map[string]string{
		"Current session":           "five_hour",
		"Current week (all models)": "weekly",
		"Weekly":                    "weekly",
		"Monthly":                   "monthly",
		"5-hour limit":              "five_hour",
	} {
		spec, ok := specFor(line)
		if !ok || spec.id != want {
			t.Errorf("%q -> %+v/%v, want %s", line, spec, ok, want)
		}
	}
}
