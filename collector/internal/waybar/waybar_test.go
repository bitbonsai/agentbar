package waybar

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentbar/collector/internal/schema"
)

func snapWith(pct float64, status string) *schema.Snapshot {
	resets := 144*60 + 24
	s := schema.NewSnapshot()
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.Providers = []schema.Provider{{
		ID: "antigravity", Name: "Antigravity", Account: "a@b.c", Status: status,
		Groups: []schema.ModelGroup{{
			ID: "gemini-models", Name: "Gemini Models",
			Windows: []schema.QuotaWindow{
				{ID: "weekly", Label: "Weekly", RemainingPct: pct, ResetsInMinutes: &resets},
				{ID: "five_hour", Label: "5h", RemainingPct: 97.81, WindowMinutes: 300,
					StatusText: "Quota available"},
			},
		}},
	}}
	s.Finalize()
	return &s
}

func TestFormatClasses(t *testing.T) {
	cases := []struct {
		pct       float64
		wantClass string
	}{
		{97.81, "ok"},
		{50, "ok"},
		{49.9, "warn"},
		{20, "warn"},
		{19.9, "critical"},
		{0, "critical"},
	}
	for _, c := range cases {
		out := Format(snapWith(c.pct, schema.StatusOK))
		if out.Class != c.wantClass {
			t.Errorf("%v%% -> class %q, want %q", c.pct, out.Class, c.wantClass)
		}
		if out.Percentage != int(c.pct) {
			t.Errorf("%v%% -> percentage %d", c.pct, out.Percentage)
		}
	}
}

func TestFormatTextAndTooltip(t *testing.T) {
	out := Format(snapWith(39.53, schema.StatusOK))
	if !strings.Contains(out.Text, "39%") {
		t.Errorf("text = %q", out.Text)
	}
	for _, want := range []string{
		"<b>Agents</b>",
		"<b>Antigravity</b>",
		"a@b.c",
		"<b>Gemini Models</b>",
		"Weekly",
		"39.53%",
		"6d 0h",
		"5h",
		"97.81%",
		"Quota available",
	} {
		if !strings.Contains(out.Tooltip, want) {
			t.Errorf("tooltip missing %q:\n%s", want, out.Tooltip)
		}
	}
}

func TestFormatNoData(t *testing.T) {
	for _, snap := range []*schema.Snapshot{nil, {}, {Providers: []schema.Provider{}}} {
		out := Format(snap)
		if out.Text != "--" || out.Class != "error" || out.Percentage != 0 {
			t.Errorf("%+v -> %+v", snap, out)
		}
	}
}

func TestFormatNotSignedIn(t *testing.T) {
	s := schema.NewSnapshot()
	s.Providers = []schema.Provider{{ID: "antigravity", Name: "Antigravity", Status: schema.StatusNotSignedIn}}
	out := Format(&s)
	if out.Text != "--" || out.Class != "error" {
		t.Errorf("got %+v", out)
	}
	if !strings.Contains(out.Tooltip, "not signed in") {
		t.Errorf("tooltip = %q", out.Tooltip)
	}
}

func TestFormatEscapesPango(t *testing.T) {
	s := snapWith(80, schema.StatusOK)
	s.Providers[0].Account = "a&b<c>"
	out := Format(s)
	if strings.Contains(out.Tooltip, "a&b<c>") {
		t.Errorf("markup not escaped: %q", out.Tooltip)
	}
	if !strings.Contains(out.Tooltip, "a&amp;b&lt;c&gt;") {
		t.Errorf("escaped text missing: %q", out.Tooltip)
	}
}

func TestJSONShape(t *testing.T) {
	data, err := json.Marshal(Format(snapWith(39.53, schema.StatusOK)))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"text", "tooltip", "class", "percentage"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q in %s", key, data)
		}
	}
}

func TestFormatMinutes(t *testing.T) {
	cases := map[int]string{
		0:           "Ready",
		-5:          "Ready",
		45:          "45m",
		154:         "2h 34m",
		144 * 60:    "6d 0h",
		144*60 + 24: "6d 0h",
		160*60 + 58: "6d 16h",
	}
	for in, want := range cases {
		if got := formatMinutes(in); got != want {
			t.Errorf("%d -> %q, want %q", in, got, want)
		}
	}
}

func TestFormatUsesLowestAcrossProviders(t *testing.T) {
	s := snapWith(80, schema.StatusOK)
	s.Providers = append(s.Providers, schema.Provider{
		ID: "codex", Name: "Codex", Status: schema.StatusOK,
		Groups: []schema.ModelGroup{{
			ID: "codex", Name: "Codex",
			Windows: []schema.QuotaWindow{
				{ID: "weekly", Label: "Weekly", RemainingPct: 11, WindowMinutes: 10080},
			},
		}},
	})
	s.Finalize()
	out := Format(s)
	if out.Percentage != 11 {
		t.Errorf("percentage = %d, want 11 (lowest across providers)", out.Percentage)
	}
	if out.Class != "critical" {
		t.Errorf("class = %q, want critical", out.Class)
	}
	for _, want := range []string{"<b>Antigravity</b>", "<b>Codex</b>"} {
		if !strings.Contains(out.Tooltip, want) {
			t.Errorf("tooltip missing %q:\n%s", want, out.Tooltip)
		}
	}
}

func TestFormatSkipsFailedProvidersForPercentage(t *testing.T) {
	s := snapWith(80, schema.StatusOK)
	s.Providers = append(s.Providers, schema.Provider{
		ID: "claude", Name: "Claude Code", Status: schema.StatusError, Error: "boom",
	})
	out := Format(s)
	if out.Percentage != 80 {
		t.Errorf("percentage = %d, want 80", out.Percentage)
	}
	if !strings.Contains(out.Tooltip, "boom") {
		t.Errorf("tooltip = %q", out.Tooltip)
	}
}
