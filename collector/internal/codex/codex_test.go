package codex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"agentbar/collector/internal/schema"
)

func TestBuildWeeklyPrimary(t *testing.T) {
	now := time.Now()
	reset := now.Add(72 * time.Hour).Unix()
	rl := &rateLimits{
		Primary:  &limitWindow{UsedPercent: 89, WindowMinutes: 10080, ResetsAt: reset},
		PlanType: "plus",
	}
	p := Build(rl, now)

	if p.ID != "codex" || p.Name != "Codex" || p.Account != "Plus" {
		t.Fatalf("provider = %+v", p)
	}
	if len(p.Groups) != 1 || len(p.Groups[0].Windows) != 1 {
		t.Fatalf("groups = %+v", p.Groups)
	}
	w := p.Groups[0].Windows[0]
	if w.ID != "weekly" || w.Label != "Weekly" || w.WindowMinutes != 10080 {
		t.Errorf("window = %s/%s/%dm", w.ID, w.Label, w.WindowMinutes)
	}
	if w.RemainingPct != 11 {
		t.Errorf("remaining = %v, want 11", w.RemainingPct)
	}
	if w.ResetsInMinutes == nil || *w.ResetsInMinutes < 71*60 || *w.ResetsInMinutes > 73*60 {
		t.Errorf("resets = %v, want ~4320", w.ResetsInMinutes)
	}
}

func TestBuildPrimaryFiveHourAndSecondaryWeekly(t *testing.T) {
	now := time.Now()
	rl := &rateLimits{
		Primary:   &limitWindow{UsedPercent: 40, WindowMinutes: 300, ResetsAt: now.Add(2 * time.Hour).Unix()},
		Secondary: &limitWindow{UsedPercent: 20, WindowMinutes: 10080, ResetsAt: now.Add(100 * time.Hour).Unix()},
	}
	p := Build(rl, now)
	ws := p.Groups[0].Windows
	if len(ws) != 2 {
		t.Fatalf("got %d windows", len(ws))
	}
	if ws[0].ID != "five_hour" || ws[0].Label != "5h" || ws[0].RemainingPct != 60 {
		t.Errorf("primary = %+v", ws[0])
	}
	if ws[1].ID != "weekly" || ws[1].RemainingPct != 80 {
		t.Errorf("secondary = %+v", ws[1])
	}
}

func TestBuildPassedResetRefillsQuota(t *testing.T) {
	now := time.Now()
	rl := &rateLimits{
		Primary: &limitWindow{UsedPercent: 95, WindowMinutes: 300, ResetsAt: now.Add(-time.Hour).Unix()},
	}
	w := Build(rl, now).Groups[0].Windows[0]
	if w.RemainingPct != 100 || w.ResetsInMinutes != nil || w.StatusText != "Quota available" {
		t.Errorf("window = %+v, want full quota", w)
	}
}

func TestBuildDefaultsMissingWindowMinutes(t *testing.T) {
	now := time.Now()
	rl := &rateLimits{
		Primary:   &limitWindow{UsedPercent: 10, ResetsAt: now.Add(time.Hour).Unix()},
		Secondary: &limitWindow{UsedPercent: 10, ResetsAt: now.Add(time.Hour).Unix()},
	}
	ws := Build(rl, now).Groups[0].Windows
	if len(ws) != 2 || ws[0].ID != "five_hour" || ws[1].ID != "weekly" {
		t.Fatalf("windows = %+v", ws)
	}
}

func TestFileRateLimitsReadsNewestTail(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Hour).Unix()
	lines := []string{
		`{"type":"session_meta","payload":{"id":"abc"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":10,"window_minutes":300,"resets_at":1}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":25,"window_minutes":300,"resets_at":` + strconv.FormatInt(reset, 10) + `}}}}`,
	}
	path := writeFixture(t, lines)
	rl, err := fileRateLimits(path)
	if err != nil {
		t.Fatalf("fileRateLimits: %v", err)
	}
	if rl == nil || rl.Primary == nil || rl.Primary.UsedPercent != 25 {
		t.Fatalf("rate limits = %+v, want the latest 25%%", rl)
	}
}

func TestLatestRateLimitsSkipsBrokenNewestFile(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(older, []byte(`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":30,"window_minutes":300,"resets_at":99}}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(dir, "new.jsonl")
	if err := os.WriteFile(newer, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(newer, future, future); err != nil {
		t.Fatal(err)
	}
	rl, err := latestRateLimits(context.Background(), dir)
	if err != nil {
		t.Fatalf("latestRateLimits: %v", err)
	}
	if rl.Primary == nil || rl.Primary.UsedPercent != 30 {
		t.Fatalf("rate limits = %+v", rl)
	}
}

func TestFinalizeAddsPace(t *testing.T) {
	now := time.Now()
	rl := &rateLimits{Primary: &limitWindow{UsedPercent: 50, WindowMinutes: 300, ResetsAt: now.Add(150 * time.Minute).Unix()}}
	snap := schema.NewSnapshot()
	snap.Providers = append(snap.Providers, Build(rl, now))
	snap.Finalize()
	w := snap.Providers[0].Groups[0].Windows[0]
	if w.PaceRemainingPct == nil || *w.PaceRemainingPct < 49 || *w.PaceRemainingPct > 51 {
		t.Errorf("pace = %v, want ~50", w.PaceRemainingPct)
	}
}

func writeFixture(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
