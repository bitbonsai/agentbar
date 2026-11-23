package opencodego

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbar/collector/internal/schema"
)

// CLI output as returned by `opencode api GET /api/experimental/usage/limits`.
const cliPayload = `{
  "location": {"directory": "/Users/you"},
  "data": {
    "workspace": {"id": "wrk_1", "name": "Default", "usageURL": "https://opencode.ai/console/wrk_1/activity/overview"},
    "go": {
      "rolling": {"percent": 12.5, "limited": false, "resetsAt": RESET_ROLLING},
      "weekly":  {"percent": 40,   "limited": false, "resetsAt": RESET_WEEKLY},
      "monthly": {"percent": 3,    "limited": false, "resetsAt": RESET_MONTHLY}
    },
    "budgets": []
  }
}`

// Web API shape: values at the root.
const httpPayload = `{
  "workspace": {"name": "Team Workspace"},
  "rolling": {"percent": 100, "limited": true,  "resetsAt": RESET_ROLLING},
  "weekly":  {"percent": 55,  "limited": false, "resetsAt": RESET_WEEKLY},
  "monthly": {"percent": 10,  "limited": false, "resetsAt": RESET_MONTHLY}
}`

func TestParseCLIPayload(t *testing.T) {
	now := time.Now()
	payload := cliPayload
	for placeholder, offset := range map[string]time.Duration{
		"RESET_ROLLING": 3 * time.Hour,
		"RESET_WEEKLY":  48 * time.Hour,
		"RESET_MONTHLY": 480 * time.Hour,
	} {
		payload = strings.ReplaceAll(payload, placeholder, strconv.FormatInt(now.Add(offset).UnixMilli(), 10))
	}
	p, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.ID != "opencode" || p.Name != "OpenCode Go" {
		t.Fatalf("identity = %q/%q", p.ID, p.Name)
	}
	if p.Account != "Default" {
		t.Fatalf("account = %q, want Default", p.Account)
	}
	if len(p.Groups) != 1 || p.Groups[0].ID != "opencode-go" {
		t.Fatalf("groups = %+v", p.Groups)
	}
	ws := p.Groups[0].Windows
	if len(ws) != 3 {
		t.Fatalf("got %d windows, want 3", len(ws))
	}
	want := []struct {
		id      string
		label   string
		minutes int
		remain  float64
	}{
		{"five_hour", "5h", 300, 87.5},
		{"weekly", "Weekly", 10080, 60},
		{"monthly", "Monthly", 43200, 97},
	}
	for i, w := range want {
		got := ws[i]
		if got.ID != w.id || got.Label != w.label || got.WindowMinutes != w.minutes {
			t.Errorf("window %d = %s/%s/%dm, want %s/%s/%dm",
				i, got.ID, got.Label, got.WindowMinutes, w.id, w.label, w.minutes)
		}
		if got.RemainingPct != w.remain {
			t.Errorf("window %s remaining = %v, want %v", w.id, got.RemainingPct, w.remain)
		}
		if got.ResetsInMinutes == nil || *got.ResetsInMinutes <= 0 {
			t.Errorf("window %s missing reset minutes", w.id)
		}
	}
}

func TestParseHTTPPayload(t *testing.T) {
	now := time.Now()
	payload := httpPayload
	for placeholder, offset := range map[string]time.Duration{
		"RESET_ROLLING": -time.Hour, // already reset
		"RESET_WEEKLY":  48 * time.Hour,
		"RESET_MONTHLY": 480 * time.Hour,
	} {
		payload = strings.ReplaceAll(payload, placeholder, strconv.FormatInt(now.Add(offset).UnixMilli(), 10))
	}

	p, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Account != "Team Workspace" {
		t.Fatalf("account = %q", p.Account)
	}
	if len(p.Groups) != 1 || len(p.Groups[0].Windows) != 3 {
		t.Fatalf("groups = %+v", p.Groups)
	}
	rolling := p.Groups[0].Windows[0]
	if rolling.RemainingPct != 100 || rolling.ResetsInMinutes != nil {
		t.Errorf("passed-reset window = %+v, want full quota", rolling)
	}

	// A passed reset means the window refilled.
	snap := schema.NewSnapshot()
	snap.Providers = append(snap.Providers, p)
	snap.Finalize()
	refilled := snap.Providers[0].Groups[0].Windows[0]
	if refilled.RemainingPct != 100 || refilled.ResetsInMinutes != nil || refilled.StatusText != "Quota available" {
		t.Errorf("refilled window = %+v", refilled)
	}
	if refilled.PaceRemainingPct != nil {
		t.Errorf("refilled window should have no pace marker")
	}
}

func TestParseRejectsEmptyLimits(t *testing.T) {
	if _, err := Parse([]byte(`{"data":{"workspace":{"name":"Default"},"go":{}}}`)); err == nil {
		t.Fatal("want error for empty limits")
	}
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Fatal("want error for invalid json")
	}
}
