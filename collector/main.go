// agentbar-collector gathers AI quota usage into a normalized JSON snapshot
// and renders it for Waybar.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"agentbar/collector/internal/agy"
	"agentbar/collector/internal/cache"
	"agentbar/collector/internal/claude"
	"agentbar/collector/internal/codex"
	"agentbar/collector/internal/history"
	"agentbar/collector/internal/opencodego"
	"agentbar/collector/internal/schema"
	"agentbar/collector/internal/waybar"
)

func main() {
	var (
		asJSON    = flag.Bool("json", true, "print normalized JSON")
		asWaybar  = flag.Bool("waybar", false, "print Waybar module JSON")
		refresh   = flag.Bool("refresh", false, "ignore the cache and query agy")
		cacheTTL  = flag.Duration("cache-ttl", cache.DefaultTTL, "cache freshness window")
		timeout   = flag.Duration("timeout", 0, "per-provider query timeout (0 = provider default)")
		parseFile = flag.String("parse-file", "", "parse a saved agy screen dump instead of running agy")
		dbPath    = flag.String("db", "", "SQLite history path (default: per-user data dir)")
		noHistory = flag.Bool("no-history", false, "do not record usage history to SQLite")
		pastUsage = flag.Duration("history", 0, "print SQLite samples from the last duration as JSON, then exit")
	)
	flag.Parse()

	path := *dbPath
	if path == "" {
		if p, pErr := history.DefaultPath(); pErr == nil {
			path = p
		} else {
			fmt.Fprintln(os.Stderr, "agentbar: resolve history path:", pErr)
		}
	}

	if *pastUsage > 0 {
		if err := printHistory(path, *pastUsage); err != nil {
			fmt.Fprintln(os.Stderr, "agentbar: read history:", err)
			os.Exit(1)
		}
		return
	}

	var (
		snap  *schema.Snapshot
		err   error
		fresh bool
	)
	switch {
	case *parseFile != "":
		snap, err = snapshotFromFile(*parseFile)
	default:
		snap, fresh, err = collect(*refresh, *cacheTTL, *timeout)
	}
	if err != nil && snap == nil {
		s := schema.NewSnapshot()
		s.Providers = append(s.Providers, schema.Provider{
			ID: "agentbar", Name: "Agents",
			Status: schema.StatusError, Error: err.Error(),
			Groups: []schema.ModelGroup{},
		})
		snap = &s
	}

	// Recording is best-effort: stdout stays pure JSON either way.
	if fresh && !*noHistory && path != "" {
		recordHistory(path, *snap)
	}

	if *asWaybar {
		out, mErr := json.Marshal(waybar.Format(snap))
		if mErr != nil {
			fmt.Fprintln(os.Stderr, "agentbar: encode waybar output:", mErr)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}
	if !*asJSON {
		fmt.Fprintln(os.Stderr, "agentbar: nothing to print (pass --json or --waybar)")
		os.Exit(2)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if mErr := enc.Encode(snap); mErr != nil {
		fmt.Fprintln(os.Stderr, "agentbar: encode json:", mErr)
		os.Exit(1)
	}
}

// source is one provider collector plus the check that decides whether this
// machine has the tool or data directory at all.
type source struct {
	id        string
	name      string
	available bool
	collect   func(ctx context.Context, timeout time.Duration) (schema.Provider, error)
}

// sources returns the collectors in stable display order. Providers whose CLI
// or data directory is missing are skipped so the HUD stays uncluttered.
func sources() []source {
	out := make([]source, 0, 4)

	_, agyErr := agy.FindBinary()
	out = append(out, source{
		id: "antigravity", name: "Antigravity",
		available: agyErr == nil,
		collect:   agy.Collect,
	})

	_, ocErr := opencodego.FindBinary()
	out = append(out, source{
		id: "opencode", name: "OpenCode Go",
		available: ocErr == nil || os.Getenv("OPENCODE_GO_API_KEY") != "",
		collect:   opencodego.Collect,
	})

	_, claudeErr := claude.FindBinary()
	out = append(out, source{
		id: "claude", name: "Claude Code",
		available: claudeErr == nil,
		collect:   claude.Collect,
	})

	out = append(out, source{
		id: "codex", name: "Codex",
		available: codex.Available(),
		collect:   codex.Collect,
	})

	return out
}

// overallBudget bounds a whole refresh: the slowest provider (Claude Code's
// PTY) plus slack, so a wedged CLI cannot block the menubar forever.
const overallBudget = 90 * time.Second

func collect(refresh bool, ttl, timeout time.Duration) (snap *schema.Snapshot, fresh bool, err error) {
	c, cacheErr := cache.New(ttl)
	if cacheErr == nil && !refresh {
		if cached, ok := c.Load(); ok {
			return cached, false, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), overallBudget)
	defer cancel()

	list := make([]source, 0, 4)
	for _, s := range sources() {
		if s.available {
			list = append(list, s)
		}
	}
	if len(list) == 0 {
		return nil, false, errors.New("no supported agent CLIs found")
	}

	providers := make([]schema.Provider, len(list))
	errs := make([]error, len(list))
	var wg sync.WaitGroup
	for i, src := range list {
		wg.Add(1)
		go func(i int, src source) {
			defer wg.Done()
			providers[i], errs[i] = src.collect(ctx, timeout)
		}(i, src)
	}
	wg.Wait()

	s := schema.NewSnapshot()
	s.Providers = append(s.Providers, providers...)
	s.Finalize()

	anyOK := false
	for _, p := range providers {
		if p.Status == schema.StatusOK {
			anyOK = true
		}
	}

	// Keep the last good snapshot when every provider fails transiently.
	if !anyOK && cacheErr == nil {
		if stale, ok := c.LoadAny(); ok && hasOK(stale) {
			return stale, false, firstError(errs)
		}
	}
	if cacheErr == nil && anyOK {
		_ = c.Save(&s)
	}
	return &s, true, firstError(errs)
}

func hasOK(snap *schema.Snapshot) bool {
	for _, p := range snap.Providers {
		if p.Status == schema.StatusOK {
			return true
		}
	}
	return false
}

func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// recordHistory appends the snapshot to SQLite, warning on stderr on failure.
func recordHistory(path string, snap schema.Snapshot) {
	store, err := history.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentbar: open history:", err)
		return
	}
	defer func() { _ = store.Close() }()
	if _, err := store.Record(snap); err != nil {
		fmt.Fprintln(os.Stderr, "agentbar: record history:", err)
	}
}

// printHistory dumps recent samples as JSON for CLI inspection.
func printHistory(path string, since time.Duration) error {
	store, err := history.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	samples, err := store.QueryRecent(since, "")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(samples)
}

func snapshotFromFile(path string) (*schema.Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := agy.Parse(string(raw))
	snap := schema.NewSnapshot()
	snap.Providers = append(snap.Providers, p)
	snap.Finalize()
	if p.Status == schema.StatusError {
		return &snap, fmt.Errorf("%s", p.Error)
	}
	return &snap, nil
}
