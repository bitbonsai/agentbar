// Package codex reads Codex CLI rate limits from its local session logs. The
// CLI records a `token_count` event with `rate_limits` on every turn, so the
// newest session file holds the latest quota picture without any network call.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentbar/collector/internal/schema"
)

// DefaultTimeout bounds the local session scan.
const DefaultTimeout = 6 * time.Second

const (
	// maxTailBytes bounds how much of a session file we parse. The newest
	// token_count event is always near the end.
	maxTailBytes = 512 * 1024
	// maxFiles bounds the scan when a machine has years of sessions.
	maxFiles = 40
)

// SessionsDir returns ~/.codex/sessions.
func SessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// Available reports whether the CLI has ever written a session on this machine.
func Available() bool {
	dir, err := SessionsDir()
	if err != nil {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

type rateLimits struct {
	LimitID     string       `json:"limit_id"`
	Primary     *limitWindow `json:"primary"`
	Secondary   *limitWindow `json:"secondary"`
	PlanType    string       `json:"plan_type"`
	RateLimited *string      `json:"rate_limit_reached_type"`
}

type limitWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

type entry struct {
	Type    string `json:"type"`
	Payload *struct {
		Type       string      `json:"type"`
		RateLimits *rateLimits `json:"rate_limits"`
	} `json:"payload"`
	RateLimits *rateLimits `json:"rate_limits"`
}

// Collect scans the newest session files for the latest rate limits.
func Collect(ctx context.Context, timeout time.Duration) (schema.Provider, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dir, err := SessionsDir()
	if err != nil {
		return errorProvider(err), err
	}

	rl, err := latestRateLimits(ctx, dir)
	if err != nil {
		return errorProvider(err), err
	}
	return Build(rl, time.Now()), nil
}

func latestRateLimits(ctx context.Context, dir string) (*rateLimits, error) {
	files, err := sessionFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no codex session files found")
	}
	for _, path := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rl, err := fileRateLimits(path)
		if err != nil || rl == nil {
			continue
		}
		return rl, nil
	}
	return nil, errors.New("no rate limits in recent codex sessions")
}

// sessionFiles returns .jsonl paths newest first, capped at maxFiles.
func sessionFiles(root string) ([]string, error) {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		files = append(files, file{path, info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths, nil
}

// fileRateLimits reads the file tail and returns the newest rate limits.
func fileRateLimits(path string) (*rateLimits, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := st.Size() - maxTailBytes
	skipFirst := false
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		skipFirst = true
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if skipFirst && len(lines) > 0 {
		lines = lines[1:] // partial line from the seek offset
	}

	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"rate_limits"`) {
			continue
		}
		var e entry
		if err := json.Unmarshal([]byte(lines[i]), &e); err != nil {
			continue
		}
		if e.Payload != nil && e.Payload.RateLimits != nil {
			return e.Payload.RateLimits, nil
		}
		if e.RateLimits != nil {
			return e.RateLimits, nil
		}
	}
	return nil, nil
}

// Build maps rate limits into a provider. Primary/secondary are labeled by
// their real window length: this machine reports the weekly quota as primary.
func Build(rl *rateLimits, now time.Time) schema.Provider {
	p := schema.Provider{
		ID:      "codex",
		Name:    "Codex",
		Account: planLabel(rl.PlanType),
		Status:  schema.StatusOK,
		Groups:  []schema.ModelGroup{},
	}
	if rl == nil {
		p.Status = schema.StatusError
		p.Error = "no codex rate limits"
		return p
	}

	group := schema.ModelGroup{ID: "codex", Name: "Codex"}
	seen := map[string]bool{}
	for _, w := range []struct {
		limit    *limitWindow
		fallback string
	}{
		{rl.Primary, "five_hour"},
		{rl.Secondary, "weekly"},
	} {
		if w.limit == nil {
			continue
		}
		id, label, minutes := windowSpec(w.limit.WindowMinutes, w.fallback)
		if seen[id] {
			continue
		}
		seen[id] = true
		group.Windows = append(group.Windows, buildWindow(w.limit, id, label, minutes, now))
	}
	if len(group.Windows) == 0 {
		p.Status = schema.StatusError
		p.Error = "codex rate limits have no windows"
		return p
	}
	p.Groups = append(p.Groups, group)
	return p
}

func windowSpec(minutes int, fallback string) (id, label string, windowMinutes int) {
	switch {
	case minutes <= 0:
		if fallback == "weekly" {
			return "weekly", "Weekly", schema.WeeklyWindowMinutes
		}
		return "five_hour", "5h", schema.FiveHourWindowMinutes
	case minutes <= 6*60:
		return "five_hour", "5h", minutes
	case minutes >= 14*24*60:
		return "monthly", "Monthly", minutes
	default:
		return "weekly", "Weekly", minutes
	}
}

func buildWindow(l *limitWindow, id, label string, minutes int, now time.Time) schema.QuotaWindow {
	w := schema.QuotaWindow{
		ID:            id,
		Label:         label,
		RawLabel:      label,
		RemainingPct:  clamp(100 - l.UsedPercent),
		UsedPct:       clamp(l.UsedPercent),
		WindowMinutes: minutes,
	}
	if l.ResetsAt > 0 {
		mins := int(time.Unix(l.ResetsAt, 0).Sub(now).Minutes())
		if mins <= 0 {
			w.RemainingPct = 100
			w.StatusText = "Quota available"
		} else {
			w.ResetsInMinutes = &mins
		}
	}
	return w
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

func planLabel(plan string) string {
	switch strings.ToLower(plan) {
	case "":
		return ""
	case "plus":
		return "Plus"
	case "pro":
		return "Pro"
	case "team":
		return "Team"
	case "free":
		return "Free"
	default:
		return strings.ToUpper(plan[:1]) + plan[1:]
	}
}

func errorProvider(err error) schema.Provider {
	return schema.Provider{
		ID:     "codex",
		Name:   "Codex",
		Status: schema.StatusError,
		Error:  err.Error(),
		Groups: []schema.ModelGroup{},
	}
}
