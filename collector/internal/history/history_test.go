package history

import (
	"path/filepath"
	"testing"
	"time"

	"agentbar/collector/internal/schema"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "dir", "history.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func snapshotAt(at time.Time, providerID, status string, remaining float64) schema.Snapshot {
	reset := 120
	return schema.Snapshot{
		UpdatedAt: at.UTC().Format(time.RFC3339),
		Providers: []schema.Provider{{
			ID: providerID, Name: "Antigravity", Account: "me@example.com", Status: status,
			Groups: []schema.ModelGroup{{
				ID:   "gemini-models",
				Name: "Gemini Models",
				Windows: []schema.QuotaWindow{{
					ID: "weekly", Label: "Weekly", RawLabel: "Weekly Limit Remaining",
					RemainingPct: remaining, UsedPct: 100 - remaining,
					ResetsInMinutes: &reset, WindowMinutes: schema.WeeklyWindowMinutes,
				}},
			}},
		}},
	}
}

func TestRecordAllGroupsAndWindows(t *testing.T) {
	store := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)
	snap := snapshotAt(now, "antigravity", schema.StatusOK, 42.5)
	snap.Providers[0].Groups = append(snap.Providers[0].Groups, schema.ModelGroup{
		ID: "claude-models", Name: "Claude Models",
		Windows: []schema.QuotaWindow{
			{ID: "5h", Label: "5h", RemainingPct: 80},
			{ID: "weekly", Label: "Weekly", RemainingPct: 60},
		},
	})
	snap.Finalize()

	inserted, err := store.Record(snap)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if inserted != 3 {
		t.Fatalf("inserted = %d, want 3", inserted)
	}

	samples, err := store.QueryRecent(time.Hour, "")
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3", len(samples))
	}
	first := samples[0]
	if first.ProviderID != "antigravity" || first.GroupID != "gemini-models" ||
		first.WindowID != "weekly" || first.Account != "me@example.com" {
		t.Fatalf("unexpected first sample: %+v", first)
	}
	if first.RemainingPct != 42.5 || first.UsedPct != 57.5 {
		t.Fatalf("unexpected percentages: %+v", first)
	}
	if first.ResetsInMinutes == nil || *first.ResetsInMinutes != 120 {
		t.Fatalf("resetsInMinutes = %v, want 120", first.ResetsInMinutes)
	}
	if first.PaceRemainingPct == nil || *first.PaceRemainingPct != 1.19 {
		t.Fatalf("paceRemainingPct = %v, want 1.19", first.PaceRemainingPct)
	}

	// Ordered oldest first.
	for i := 1; i < len(samples); i++ {
		if samples[i-1].RecordedAt > samples[i].RecordedAt {
			t.Fatalf("samples not ordered by recorded_at: %q then %q",
				samples[i-1].RecordedAt, samples[i].RecordedAt)
		}
	}
}

func TestRecordDedup(t *testing.T) {
	store := openTemp(t)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	steps := []struct {
		name      string
		at        time.Duration
		remaining float64
		want      int
	}{
		{"first sample", 0, 50, 1},
		{"unchanged 10m later", 10 * time.Minute, 50, 0},
		{"rounding noise", 20 * time.Minute, 50.005, 0},
		{"changed", 25 * time.Minute, 45, 1},
		{"unchanged 29m after change", 54 * time.Minute, 45, 0},
		{"unchanged 30m after change", 55 * time.Minute, 45, 1},
		{"unchanged 31m later", 86 * time.Minute, 45, 1},
	}
	for _, step := range steps {
		snap := snapshotAt(base.Add(step.at), "antigravity", schema.StatusOK, step.remaining)
		inserted, err := store.Record(snap)
		if err != nil {
			t.Fatalf("%s: Record: %v", step.name, err)
		}
		if inserted != step.want {
			t.Fatalf("%s: inserted = %d, want %d", step.name, inserted, step.want)
		}
	}

	samples, err := store.QueryRecent(24*time.Hour, "")
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4", len(samples))
	}
}

func TestRecordSkipsNotOKProviders(t *testing.T) {
	store := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, status := range []string{schema.StatusError, schema.StatusNotSignedIn} {
		inserted, err := store.Record(snapshotAt(now, "antigravity", status, 10))
		if err != nil {
			t.Fatalf("status %s: Record: %v", status, err)
		}
		if inserted != 0 {
			t.Fatalf("status %s: inserted = %d, want 0", status, inserted)
		}
	}
}

func TestQueryRecentFilters(t *testing.T) {
	store := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := store.Record(snapshotAt(now.Add(-3*time.Hour), "antigravity", schema.StatusOK, 70)); err != nil {
		t.Fatalf("Record old: %v", err)
	}
	if _, err := store.Record(snapshotAt(now.Add(-30*time.Minute), "antigravity", schema.StatusOK, 65)); err != nil {
		t.Fatalf("Record recent: %v", err)
	}
	if _, err := store.Record(snapshotAt(now.Add(-10*time.Minute), "codex", schema.StatusOK, 20)); err != nil {
		t.Fatalf("Record codex: %v", err)
	}

	recent, err := store.QueryRecent(time.Hour, "")
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("got %d recent samples, want 2", len(recent))
	}
	if recent[0].ProviderID != "antigravity" || recent[1].ProviderID != "codex" {
		t.Fatalf("unexpected provider order: %+v", recent)
	}

	onlyAntigravity, err := store.QueryRecent(24*time.Hour, "antigravity")
	if err != nil {
		t.Fatalf("QueryRecent provider: %v", err)
	}
	if len(onlyAntigravity) != 2 {
		t.Fatalf("got %d antigravity samples, want 2", len(onlyAntigravity))
	}

	empty, err := store.QueryRecent(time.Hour, "nope")
	if err != nil {
		t.Fatalf("QueryRecent unknown provider: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("got %v, want empty non-nil slice", empty)
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if filepath.Base(path) != "history.db" {
		t.Fatalf("DefaultPath = %q, want .../history.db", path)
	}
}
