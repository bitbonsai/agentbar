package claude

import (
	"regexp"
	"strconv"
	"strings"

	"agentbar/collector/internal/schema"
)

var (
	csiRe    = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[a-zA-Z]|\x1b[()][A-Z0-9]|\x1b[=>78]`)
	pctRe    = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*%`)
	durRe    = regexp.MustCompile(`(\d+)\s*([dhm])`)
	acctTail = regexp.MustCompile(`·\s*([A-Za-z][^·\n]*)$`)
)

// StripANSI removes terminal escape sequences and normalizes line endings.
func StripANSI(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = csiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// Parse turns `claude` /usage output into a normalized provider. Claude Code
// prints its plan limits as "<label>" followed by "NN% used" and a reset line;
// accounts on API billing show no percentages, which yields a provider with no
// quota groups rather than an error.
func Parse(raw string) schema.Provider {
	text := StripANSI(raw)
	lines := strings.Split(text, "\n")

	p := schema.Provider{
		ID:     "claude",
		Name:   "Claude Code",
		Status: schema.StatusOK,
		Groups: []schema.ModelGroup{},
	}

	group := schema.ModelGroup{ID: "claude-plan", Name: "Claude Code"}
	seen := map[string]*schema.QuotaWindow{}

	pending := &windowSpec{}
	cooldown := 0
	var last *schema.QuotaWindow

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if p.Account == "" {
			if tail := accountFrom(line); tail != "" {
				p.Account = tail
			}
		}
		if cooldown > 0 {
			cooldown--
		}

		if spec, ok := specFor(line); ok {
			*pending = spec
			cooldown = 6
			last = nil
			continue
		}

		if m := pctRe.FindStringSubmatch(line); m != nil {
			if cooldown == 0 || pending.id == "" {
				continue
			}
			if _, dup := seen[pending.id]; dup {
				continue
			}
			pct, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				continue
			}
			remaining := 100 - pct
			if lower := strings.ToLower(line); strings.Contains(lower, "left") ||
				strings.Contains(lower, "remaining") {
				remaining = pct
			}
			w := &schema.QuotaWindow{
				ID:            pending.id,
				Label:         pending.label,
				RawLabel:      pending.label,
				RemainingPct:  clamp(remaining),
				UsedPct:       clamp(100 - remaining),
				WindowMinutes: pending.minutes,
			}
			if status := resetStatus(line); status != "" {
				w.StatusText = status
			}
			if resets := parseResets(line); resets != nil {
				w.ResetsInMinutes = resets
			}
			seen[pending.id] = w
			last = w
			*pending = windowSpec{}
			cooldown = 4
			continue
		}

		if last != nil && strings.HasPrefix(strings.ToLower(line), "resets") {
			if status := resetStatus(line); status != "" {
				last.StatusText = status
			}
			if resets := parseResets(line); resets != nil {
				last.ResetsInMinutes = resets
			}
		}
	}

	for _, id := range []string{"five_hour", "weekly", "monthly"} {
		if w, ok := seen[id]; ok {
			group.Windows = append(group.Windows, *w)
		}
	}
	// Preserve the on-screen order for any unknown window ids.
	for id, w := range seen {
		switch id {
		case "five_hour", "weekly", "monthly":
			continue
		}
		group.Windows = append(group.Windows, *w)
	}
	if len(group.Windows) > 0 {
		p.Groups = append(p.Groups, group)
	}
	return p
}

type windowSpec struct {
	id      string
	label   string
	minutes int
}

func specFor(line string) (windowSpec, bool) {
	l := strings.ToLower(line)
	// Skip account/status lines that merely mention a session.
	if strings.Contains(l, ":") && !strings.Contains(l, "resets") {
		return windowSpec{}, false
	}
	switch {
	case strings.Contains(l, "current session"), strings.Contains(l, "five hour"),
		strings.Contains(l, "5-hour"), strings.Contains(l, "5 hour"),
		strings.HasPrefix(l, "session"):
		return windowSpec{"five_hour", "5h", schema.FiveHourWindowMinutes}, true
	case strings.Contains(l, "current week"), strings.Contains(l, "weekly"),
		strings.HasPrefix(l, "week"):
		return windowSpec{"weekly", "Weekly", schema.WeeklyWindowMinutes}, true
	case strings.Contains(l, "monthly"):
		return windowSpec{"monthly", "Monthly", 30 * 24 * 60}, true
	}
	return windowSpec{}, false
}

// resetStatus keeps a human readable reset line when we cannot parse minutes.
func resetStatus(line string) string {
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, "resets") {
		return ""
	}
	trimmed := strings.TrimSpace(line)
	if strings.Contains(strings.ToLower(trimmed), "in ") {
		return ""
	}
	return trimmed
}

// parseResets handles "Resets in 2h 15m" and "Resets in 3 days".
func parseResets(line string) *int {
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, "resets") || !strings.Contains(lower, " in ") {
		return nil
	}
	idx := strings.Index(lower, " in ")
	duration := line[idx+len(" in "):]
	total, found := 0, false
	for _, m := range durRe.FindAllStringSubmatch(duration, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		found = true
		switch m[2] {
		case "d":
			total += n * 24 * 60
		case "h":
			total += n * 60
		case "m":
			total += n
		}
	}
	if !found {
		return nil
	}
	return &total
}

func accountFrom(line string) string {
	if !strings.Contains(line, "Billing") && !strings.Contains(line, "plan") &&
		!strings.Contains(line, "Plan") {
		return ""
	}
	m := acctTail.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func clamp(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
