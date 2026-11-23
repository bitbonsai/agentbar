// Package agy drives the Antigravity CLI (`agy`) in a pseudo-terminal and
// parses its /usage screen into the normalized schema.
package agy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"agentbar/collector/internal/schema"
)

const (
	DefaultTimeout = 15 * time.Second
	// 60x140 keeps the whole /usage screen on one page so nothing scrolls out.
	ptyRows = 60
	ptyCols = 140

	trustPromptText = "Do you trust the contents of this project"
	usageScreenText = "Models & Quota"
	readyPromptText = "? for shortcuts"
)

var errNotSignedIn = errors.New("agy is not signed in")

// FindBinary returns the agy path from $PATH or standard install directories.
func FindBinary() (string, error) {
	if p, err := exec.LookPath("agy"); err == nil {
		return p, nil
	}
	candidates := []string{
		"/opt/homebrew/bin/agy",
		"/usr/local/bin/agy",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{filepath.Join(home, ".local", "bin", "agy")}, candidates...)
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("agy not found in $PATH, ~/.local/bin, /opt/homebrew/bin, or /usr/local/bin")
}

// workspace returns a dedicated, empty directory for agy to run in. Using a
// scratch dir means the trust prompt (if any) covers nothing sensitive.
func workspace() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "agentbar", "workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// childEnv strips Agy/Gemini/Antigravity vars so a caller's API-key
// experiments don't change which account the CLI reports, and ensures common
// bin directories are on PATH when launched from a GUI app.
func childEnv() []string {
	out := make([]string, 0, len(os.Environ())+2)
	pathVal := ""
	for _, kv := range os.Environ() {
		key := kv
		val := ""
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
			val = kv[i+1:]
		}
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GEMINI_") || strings.HasPrefix(upper, "AGY_") ||
			strings.HasPrefix(upper, "ANTIGRAVITY_") {
			continue
		}
		if key == "PATH" {
			pathVal = val
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+augmentedPath(pathVal), "TERM=xterm-256color")
}

func augmentedPath(existing string) string {
	extra := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		extra = append(extra, filepath.Join(home, ".local", "bin"))
	}
	parts := []string{}
	seen := map[string]bool{}
	for _, p := range filepath.SplitList(existing) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	for _, p := range extra {
		if !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// RunUsage spawns agy, navigates to /usage, and returns the raw screen text.
func RunUsage(ctx context.Context, timeout time.Duration) (string, error) {
	bin, err := FindBinary()
	if err != nil {
		return "", err
	}
	dir, err := workspace()
	if err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	deadline := time.Now().Add(timeout)

	cmd := exec.Command(bin)
	cmd.Env = childEnv()
	cmd.Dir = dir

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyRows, Cols: ptyCols})
	if err != nil {
		return "", fmt.Errorf("start agy: %w", err)
	}
	defer func() {
		_ = f.Close()
		killProcessGroup(cmd)
	}()

	var mu sync.Mutex
	var buf bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		tmp := make([]byte, 32*1024)
		for {
			n, err := f.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	screen := func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
	send := func(s string) error {
		if _, err := f.Write([]byte(s)); err != nil {
			return fmt.Errorf("write to agy: %w", err)
		}
		return nil
	}

	// Wait for the first paint, then clear the folder-trust gate. The
	// workspace is an empty agentbar-owned dir, so trusting it grants nothing.
	if err := pollUntil(ctx, deadline, 8*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, trustPromptText) || strings.Contains(s, readyPromptText) ||
			strings.Contains(s, usageScreenText)
	}); err != nil {
		return screen(), err
	}
	if strings.Contains(screen(), trustPromptText) {
		if err := send("\r"); err != nil {
			return screen(), err
		}
		_ = pollUntil(ctx, deadline, 5*time.Second, func() bool {
			return strings.Contains(screen(), readyPromptText)
		})
		_ = waitFor(ctx, 300*time.Millisecond)
	}

	if !strings.Contains(screen(), usageScreenText) {
		if err := send("/usage"); err != nil {
			return screen(), err
		}
		if !waitFor(ctx, 250*time.Millisecond) {
			return screen(), ctx.Err()
		}
		if err := send("\r"); err != nil {
			return screen(), err
		}
	}

	// Poll for the quota screen; some builds need one extra Enter while the
	// slash-command menu settles.
	typed := time.Now()
	nudged := false
	for {
		s := screen()
		if strings.Contains(s, usageScreenText) && strings.Contains(s, "%") {
			break
		}
		if time.Now().After(deadline) {
			if strings.Contains(strings.ToLower(s), "signing in") {
				return s, errors.New("agy is still signing in")
			}
			if strings.TrimSpace(s) == "" {
				return "", errors.New("agy produced no output")
			}
			return s, fmt.Errorf("timeout after %s waiting for /usage", timeout)
		}
		if !nudged && time.Since(typed) > 8*time.Second {
			nudged = true
			_ = send("\r")
		}
		if !waitFor(ctx, 150*time.Millisecond) {
			return s, ctx.Err()
		}
	}

	// Give the final repaint a beat, then leave the TUI cleanly.
	_ = waitFor(ctx, 500*time.Millisecond)
	out := screen()
	if path := os.Getenv("AGENTBAR_RAW_DUMP"); path != "" {
		_ = os.WriteFile(path, []byte(out), 0o600)
	}
	_ = send("\x1b")
	_ = send("\x04\x04")
	_ = f.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
	}
	return out, nil
}

// Collect runs agy and returns the normalized provider.
func Collect(ctx context.Context, timeout time.Duration) (schema.Provider, error) {
	raw, err := RunUsage(ctx, timeout)
	if err != nil && !strings.Contains(raw, usageScreenText) {
		msg := err.Error()
		return schema.Provider{
			ID: "antigravity", Name: "Antigravity",
			Status: schema.StatusError, Error: msg,
			Groups: []schema.ModelGroup{},
		}, err
	}
	p := Parse(raw)
	if p.Status == schema.StatusNotSignedIn {
		return p, errNotSignedIn
	}
	return p, nil
}

// pollUntil returns nil once cond is true, ctx.Err() if cancelled, or an
// error when the budget runs out.
func pollUntil(ctx context.Context, deadline time.Time, budget time.Duration, cond func() bool) error {
	end := time.Now().Add(budget)
	if end.After(deadline) {
		end = deadline
	}
	for {
		if cond() {
			return nil
		}
		if time.Now().After(end) {
			return errors.New("agy did not reach a usable prompt in time")
		}
		if !waitFor(ctx, 150*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func waitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
