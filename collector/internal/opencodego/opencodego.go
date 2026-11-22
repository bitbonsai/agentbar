// Package opencodego reads OpenCode Go plan limits from the `opencode` CLI or
// its web API and maps them into the normalized schema.
package opencodego

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentbar/collector/internal/schema"
)

const (
	DefaultTimeout = 6 * time.Second
	usagePath      = "/api/experimental/usage/limits"
	// DefaultUsageURL is the documented fallback when the CLI is missing.
	DefaultUsageURL = "https://opencode.ai/inference/go/v1/usage"

	rollingWindowMinutes = 5 * 60
	weeklyWindowMinutes  = 7 * 24 * 60
	monthlyWindowMinutes = 30 * 24 * 60
)

// FindBinary returns the opencode path from $PATH or ~/.bun/bin.
func FindBinary() (string, error) {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".bun", "bin", "opencode")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("opencode not found in $PATH or ~/.bun/bin")
}

// limit is one OpenCode Go quota window.
type limit struct {
	Percent  float64 `json:"percent"`
	Limited  bool    `json:"limited"`
	ResetsAt int64   `json:"resetsAt"`
}

type goLimits struct {
	Rolling *limit `json:"rolling"`
	Weekly  *limit `json:"weekly"`
	Monthly *limit `json:"monthly"`
}

type workspace struct {
	Name string `json:"name"`
}

// response accepts both shapes: the CLI nests values under data.{workspace,go},
// the web API may return them at the root.
type response struct {
	Data *struct {
		Workspace *workspace `json:"workspace"`
		Go        *goLimits  `json:"go"`
	} `json:"data"`
	Workspace *workspace `json:"workspace"`
	Go        *goLimits  `json:"go"`
	goLimits
}

// Collect queries limits via the CLI, falling back to the HTTP API when the
// CLI is missing or fails and OPENCODE_GO_API_KEY is set.
func Collect(ctx context.Context, timeout time.Duration) (schema.Provider, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin, binErr := FindBinary()
	var (
		raw []byte
		err error
	)
	if binErr == nil {
		raw, err = runCLI(ctx, bin)
	}
	if binErr != nil || err != nil {
		key := os.Getenv("OPENCODE_GO_API_KEY")
		if key == "" {
			if binErr != nil {
				return errorProvider(binErr), binErr
			}
			return errorProvider(err), err
		}
		raw, err = fetchUsage(ctx, key)
		if err != nil {
			return errorProvider(err), err
		}
	}

	p, err := Parse(raw)
	return p, err
}

func runCLI(ctx context.Context, bin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, "api", "GET", usagePath)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("opencode api: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("opencode api: %w", err)
	}
	return out, nil
}

func fetchUsage(ctx context.Context, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DefaultUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "agentbar-collector/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("usage API: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// Parse turns a limits payload into a provider.
func Parse(raw []byte) (schema.Provider, error) {
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return errorProvider(fmt.Errorf("decode opencode limits: %w", err)), err
	}

	account := ""
	switch {
	case r.Data != nil && r.Data.Workspace != nil:
		account = r.Data.Workspace.Name
	case r.Workspace != nil:
		account = r.Workspace.Name
	}
	var limits *goLimits
	switch {
	case r.Data != nil && r.Data.Go != nil:
		limits = r.Data.Go
	case r.Go != nil:
		limits = r.Go
	default:
		limits = &r.goLimits
	}

	p := schema.Provider{
		ID:      "opencode",
		Name:    "OpenCode Go",
		Account: account,
		Status:  schema.StatusOK,
		Groups:  []schema.ModelGroup{},
	}

	group := schema.ModelGroup{ID: "opencode-go", Name: "OpenCode Go"}
	now := time.Now()
	for _, w := range []struct {
		limit   *limit
		id      string
		label   string
		minutes int
	}{
		{limits.Rolling, "five_hour", "5h", rollingWindowMinutes},
		{limits.Weekly, "weekly", "Weekly", weeklyWindowMinutes},
		{limits.Monthly, "monthly", "Monthly", monthlyWindowMinutes},
	} {
		if w.limit == nil || w.limit.ResetsAt == 0 {
			continue
		}
		group.Windows = append(group.Windows, window(w.limit, w.id, w.label, w.minutes, now))
	}

	if len(group.Windows) == 0 {
		err := errors.New("no usage limits in opencode response")
		return errorProvider(err), err
	}
	p.Groups = append(p.Groups, group)
	return p, nil
}

func window(l *limit, id, label string, minutes int, now time.Time) schema.QuotaWindow {
	used := clamp(l.Percent)
	w := schema.QuotaWindow{
		ID:            id,
		Label:         label,
		RawLabel:      label,
		RemainingPct:  100 - used,
		UsedPct:       used,
		WindowMinutes: minutes,
	}
	if l.ResetsAt > 0 {
		mins := int(time.UnixMilli(l.ResetsAt).Sub(now).Minutes())
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

func errorProvider(err error) schema.Provider {
	return schema.Provider{
		ID:     "opencode",
		Name:   "OpenCode Go",
		Status: schema.StatusError,
		Error:  err.Error(),
		Groups: []schema.ModelGroup{},
	}
}
