// Package claude reads Claude Code plan limits by driving the `claude` CLI in a
// pseudo-terminal with `/usage` and parsing the percentages it prints.
package claude

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
	DefaultTimeout = 25 * time.Second
	// 50x120 fits the /usage panel without wrapping the percentage rows.
	ptyRows = 50
	ptyCols = 120

	// usageWait is how long we wait for quota percentages after /usage.
	usageWait = 12 * time.Second

	trustPromptText = "trust this folder"
	readyMarker     = "Esc to interrupt"
)

var errNotSignedIn = errors.New("claude is not signed in")

// FindBinary returns the claude path from $PATH or ~/.local/bin.
func FindBinary() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "bin", "claude")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("claude not found in $PATH or ~/.local/bin")
}

// workspace returns a dedicated, empty directory for the CLI to run in.
func workspace() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "agentbar", "claude-workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// childEnv drops nested-session markers so the child behaves like a fresh
// interactive shell instead of refusing to start inside this session.
func childEnv() []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "CLAUDECODE") || strings.HasPrefix(upper, "CLAUDE_CODE_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color")
}

// RunUsage spawns claude, opens /usage, and returns the raw screen text.
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

	cmd := exec.Command(bin, "--ax-screen-reader")
	cmd.Env = childEnv()
	cmd.Dir = dir

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyRows, Cols: ptyCols})
	if err != nil {
		return "", fmt.Errorf("start claude: %w", err)
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
	if path := os.Getenv("AGENTBAR_CLAUDE_RAW_DUMP"); path != "" {
		defer func() { _ = os.WriteFile(path, []byte(screen()), 0o600) }()
	}
	send := func(s string) error {
		if _, err := f.Write([]byte(s)); err != nil {
			return fmt.Errorf("write to claude: %w", err)
		}
		return nil
	}

	// Wait for the first paint, then accept the folder-trust gate. The
	// workspace is an empty agentbar-owned dir, so trusting it grants nothing.
	if err := pollUntil(ctx, deadline, 12*time.Second, func() bool {
		s := strings.ToLower(screen())
		return strings.Contains(s, trustPromptText) || strings.Contains(screen(), readyMarker) ||
			strings.Contains(s, "manual mode on") || strings.Contains(s, "effort:")
	}); err != nil {
		return screen(), err
	}
	if lower := strings.ToLower(screen()); strings.Contains(lower, trustPromptText) {
		// Screen-reader mode asks "Enter y/n:"; the TUI variant starts on
		// "No, exit" so it needs one move down to the trust option.
		answer := "\x1b[B\r"
		if strings.Contains(lower, "y/n") {
			answer = "y\r"
		}
		if err := send(answer); err != nil {
			return screen(), err
		}
		_ = pollUntil(ctx, deadline, 8*time.Second, func() bool {
			s := strings.ToLower(screen())
			return !strings.Contains(s, trustPromptText)
		})
	}

	if !waitFor(ctx, 500*time.Millisecond) {
		return screen(), ctx.Err()
	}
	if err := send("/usage\r"); err != nil {
		return screen(), err
	}

	// The panel opens on whichever tab suits the account. Walk left a few
	// tabs, keeping every repaint in the buffer, until the panel settles.
	typed := time.Now()
	var panelAt time.Time
	nudges := 0
	for {
		s := screen()
		if panelAt.IsZero() && (strings.Contains(s, "←/→ to switch") || strings.Contains(s, "Esc to close")) {
			panelAt = time.Now()
		}
		if !panelAt.IsZero() && time.Since(panelAt) > 1500*time.Millisecond {
			break
		}
		if strings.Contains(s, "%") && time.Since(typed) > 3*time.Second {
			break
		}
		if time.Now().After(deadline.Add(usageWait)) {
			if isSignedOut(s) {
				return s, errNotSignedIn
			}
			if strings.TrimSpace(s) == "" {
				return "", errors.New("claude produced no output")
			}
			return s, fmt.Errorf("timeout after %s waiting for /usage", timeout)
		}
		if nudges < 4 && time.Since(typed) > time.Duration(700*(nudges+1))*time.Millisecond {
			nudges++
			if err := send("\x1b[D"); err != nil {
				return s, err
			}
		}
		if !waitFor(ctx, 150*time.Millisecond) {
			return s, ctx.Err()
		}
	}
	out := screen()
	_ = send("\x1b")
	_ = send("/exit\r")
	_ = f.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
	}
	return out, nil
}

// Collect runs claude and returns the normalized provider.
func Collect(ctx context.Context, timeout time.Duration) (schema.Provider, error) {
	raw, err := RunUsage(ctx, timeout)
	p := Parse(raw)
	if len(p.Groups) > 0 {
		return p, nil
	}
	if errors.Is(err, errNotSignedIn) || isSignedOut(raw) {
		p.Status = schema.StatusNotSignedIn
		return p, errNotSignedIn
	}
	if err != nil {
		p.Status = schema.StatusError
		p.Error = err.Error()
		return p, err
	}
	return p, nil
}

func isSignedOut(raw string) bool {
	s := strings.ToLower(raw)
	for _, marker := range []string{"not logged in", "please log in", "please run /login", "run /login"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
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
			return errors.New("claude did not reach a usable prompt in time")
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
