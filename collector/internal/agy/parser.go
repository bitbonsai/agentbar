package agy

import (
	"regexp"
	"strconv"
	"strings"

	"agentbar/collector/internal/schema"
)

var (
	// Cursor movement (up/down/column/position) means "new line" for our
	// purposes: the TUI repaints rows and splices text onto earlier lines.
	cursorMoveRe = regexp.MustCompile(`\x1b\[[0-9;]*[ABCDEFGHfd]`)
	ansiRe       = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[a-zA-Z]|\x1b[()][A-Z0-9]|\x1b[=>]`)
	barRe        = regexp.MustCompile(`\[[^\]]*\]\s*([0-9]+(?:\.[0-9]+)?)\s*%`)
	durationRe   = regexp.MustCompile(`(\d+)\s*([dhm])`)
	accountRe    = regexp.MustCompile(`(?i)^account:\s*(.*)$`)
)

// StripANSI removes terminal escape sequences and normalizes line endings.
func StripANSI(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = cursorMoveRe.ReplaceAllString(s, "\n")
	s = ansiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// Parse turns raw `agy /usage` screen output into a normalized provider.
func Parse(raw string) schema.Provider {
	text := StripANSI(raw)

	p := schema.Provider{
		ID:     "antigravity",
		Name:   "Antigravity",
		Status: schema.StatusOK,
		Groups: []schema.ModelGroup{},
	}

	// Success wins over the "currently not signed in" splash line, which agy
	// prints while it signs in.
	if !strings.Contains(text, "Models & Quota") {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "not signed in") && !strings.Contains(lower, "signing in") {
			p.Status = schema.StatusNotSignedIn
			return p
		}
	}

	lines := strings.Split(text, "\n")

	var (
		group       *schema.ModelGroup
		pending     *schema.QuotaWindow // window awaiting its bar/reset lines
		havePending bool
		gotPct      bool
	)

	flush := func() {
		if group != nil && havePending && pending != nil && gotPct {
			group.Windows = append(group.Windows, *pending)
		}
		pending, havePending, gotPct = nil, false, false
	}
	flushGroup := func() {
		flush()
		if group != nil {
			if group.ID != "" && len(group.Windows) > 0 {
				p.Groups = append(p.Groups, *group)
			}
			group = nil
		}
	}

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		trimmed := strings.TrimSpace(line)

		if m := accountRe.FindStringSubmatch(trimmed); m != nil {
			p.Account = strings.TrimSpace(m[1])
			continue
		}
		if strings.Contains(trimmed, "Models & Quota") {
			continue
		}

		// Quota block header: "Weekly Limit Remaining", "Gemini 4 Argon Weekly Limit".
		if indented && isLimitHeader(trimmed) {
			flush()
			pending = newWindow(trimmed)
			havePending = true
			continue
		}

		// Bar line with percentage.
		if m := barRe.FindStringSubmatch(trimmed); m != nil && havePending && pending != nil {
			pct, _ := strconv.ParseFloat(m[1], 64)
			pending.RemainingPct = pct
			gotPct = true
			continue
		}
		if havePending && pending != nil && strings.HasPrefix(strings.ToLower(trimmed), "refreshes in ") {
			mins := parseDurationMinutes(strings.TrimSpace(trimmed[len("Refreshes in "):]))
			pending.ResetsInMinutes = mins
			pending.StatusText = "Resets in " + strings.TrimSpace(trimmed[len("Refreshes in "):])
			continue
		}
		if havePending && pending != nil && strings.Contains(strings.ToLower(trimmed), "quota available") {
			pending.StatusText = "Quota available"
			pending.ResetsInMinutes = nil
			continue
		}

		// A new group header (all caps, short). Checked before descriptions
		// because real output may indent headers.
		if looksLikeGroupHeader(trimmed) {
			flushGroup()
			group = &schema.ModelGroup{
				ID:   slug(trimmed),
				Name: displayName(trimmed),
			}
			continue
		}

		// Group description under the header.
		if group != nil && indented {
			const prefix = "Models within this group:"
			if strings.HasPrefix(trimmed, prefix) {
				group.Description = strings.TrimSpace(trimmed[len(prefix):])
			} else if group.Description == "" {
				group.Description = trimmed
			}
			continue
		}

	}
	flushGroup()

	if len(p.Groups) == 0 {
		p.Status = schema.StatusError
		p.Error = "no quota groups found in agy output"
	}
	return p
}

func isLimitHeader(line string) bool {
	l := strings.ToLower(line)
	if !strings.HasSuffix(l, "limit") && !strings.HasSuffix(l, "limit remaining") {
		return false
	}
	return strings.Contains(l, "weekly") || strings.Contains(l, "week") ||
		strings.Contains(l, "five hour") || strings.Contains(l, "5 hour") ||
		strings.Contains(l, "5h") || strings.Contains(l, "monthly")
}

func newWindow(header string) *schema.QuotaWindow {
	rawLabel := strings.TrimSpace(header)
	lower := strings.ToLower(rawLabel)
	w := &schema.QuotaWindow{RawLabel: rawLabel, ID: slug(rawLabel), Label: rawLabel}
	switch {
	case strings.Contains(lower, "weekly"), strings.Contains(lower, "week"):
		w.ID, w.Label, w.WindowMinutes = "weekly", "Weekly", schema.WeeklyWindowMinutes
	case strings.Contains(lower, "five hour"), strings.Contains(lower, "5 hour"), strings.Contains(lower, "5h"):
		w.ID, w.Label, w.WindowMinutes = "five_hour", "5h", schema.FiveHourWindowMinutes
	}
	return w
}

// parseDurationMinutes handles "144h 24m", "2h 34m", "45m", "6d 0h".
func parseDurationMinutes(s string) *int {
	total := 0
	found := false
	for _, m := range durationRe.FindAllStringSubmatch(s, -1) {
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

func looksLikeGroupHeader(line string) bool {
	if strings.HasPrefix(line, "└") || strings.HasPrefix(line, "├") || strings.HasPrefix(line, "│") {
		return false
	}
	if strings.ContainsAny(line, "[]%") {
		return false
	}
	hasLetter := false
	for _, r := range line {
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
			continue
		}
		if r >= 'a' && r <= 'z' {
			return false // prose in sentence case, not a header
		}
	}
	return hasLetter && len(line) < 48
}

func displayName(header string) string {
	words := strings.Fields(strings.ToUpper(header))
	out := make([]string, 0, len(words))
	for _, w := range words {
		if !strings.ContainsAny(w, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") {
			continue // drop spinner glyphs and stray decoration
		}
		if w == "AND" {
			out = append(out, "&")
			continue
		}
		if len(w) <= 3 { // keep short acronyms like GPT as-is
			out = append(out, w)
			continue
		}
		out = append(out, strings.ToUpper(w[:1])+strings.ToLower(w[1:]))
	}
	// "CLAUDE AND GPT MODELS" -> "Claude & GPT" but "GEMINI MODELS" stays.
	if len(out) > 2 && out[len(out)-1] == "Models" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, " ")
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
