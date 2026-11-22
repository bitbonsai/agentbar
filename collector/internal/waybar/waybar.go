// Package waybar renders a snapshot as Waybar custom-module JSON.
package waybar

import (
	"fmt"
	"html"
	"strings"
	"time"

	"agentbar/collector/internal/schema"
)

type Output struct {
	Text       string `json:"text"`
	Tooltip    string `json:"tooltip"`
	Class      string `json:"class"`
	Percentage int    `json:"percentage"`
}

// Format renders all providers; percentage and class follow the lowest
// remaining quota across every healthy provider.
func Format(snap *schema.Snapshot) Output {
	out := Output{Text: "--", Class: "error", Percentage: 0, Tooltip: "agentbar: no data"}
	if snap == nil || len(snap.Providers) == 0 {
		return out
	}

	out.Tooltip = tooltip(snap)
	if pct, ok := lowestPercentage(snap); ok {
		out.Percentage = pct
		out.Class = classFor(pct)
		out.Text = fmt.Sprintf("󰚩 %d%%", pct)
	}
	return out
}

// lowestPercentage returns the lowest remaining % across every ok provider,
// truncated (39.53 -> 39) so the bar matches the menubar label.
func lowestPercentage(snap *schema.Snapshot) (int, bool) {
	lowest := -1.0
	for _, p := range snap.Providers {
		if p.Status != schema.StatusOK {
			continue
		}
		for _, g := range p.Groups {
			for _, w := range g.Windows {
				if lowest < 0 || w.RemainingPct < lowest {
					lowest = w.RemainingPct
				}
			}
		}
	}
	if lowest < 0 {
		return 0, false
	}
	return int(lowest), true
}

func classFor(pct int) string {
	switch {
	case pct < 20:
		return "critical"
	case pct < 50:
		return "warn"
	default:
		return "ok"
	}
}

func tooltip(snap *schema.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>Agents</b> <span size=\"small\">%s</span>\n",
		html.EscapeString(updatedAgo(snap.UpdatedAt)))
	for _, p := range snap.Providers {
		fmt.Fprintf(&b, "\n<b>%s</b>\n", html.EscapeString(p.Name))
		switch p.Status {
		case schema.StatusNotSignedIn:
			b.WriteString("not signed in\n")
			continue
		case schema.StatusError:
			msg := p.Error
			if msg == "" {
				msg = "unknown error"
			}
			fmt.Fprintf(&b, "%s\n", html.EscapeString(msg))
			continue
		}
		if p.Account != "" {
			fmt.Fprintf(&b, "<span size=\"small\">%s</span>\n", html.EscapeString(p.Account))
		}
		for _, g := range p.Groups {
			fmt.Fprintf(&b, "<b>%s</b>\n", html.EscapeString(g.Name))
			for _, w := range g.Windows {
				line := fmt.Sprintf("  %-6s %6.2f%%  %s",
					html.EscapeString(w.Label), w.RemainingPct, html.EscapeString(resetText(w)))
				b.WriteString(strings.TrimRight(line, " ") + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func resetText(w schema.QuotaWindow) string {
	if w.ResetsInMinutes == nil {
		if w.StatusText != "" {
			return w.StatusText
		}
		return ""
	}
	return formatMinutes(*w.ResetsInMinutes)
}

func formatMinutes(m int) string {
	if m <= 0 {
		return "Ready"
	}
	d, rem := m/(24*60), m%(24*60)
	h, min := rem/60, rem%60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0 && min > 0:
		return fmt.Sprintf("%dh %dm", h, min)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", min)
	}
}

func updatedAgo(updatedAt string) string {
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return "updated unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "updated now"
	case d < time.Hour:
		return fmt.Sprintf("updated %dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("updated %dh ago", int(d.Hours()))
	}
}
