// Package history stores quota samples in a local SQLite database so trends
// (burn rate, reset behaviour) survive beyond the single-snapshot cache.
// Uses modernc.org/sqlite, a pure-Go driver, to keep CGO off for cross builds.
package history

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"agentbar/collector/internal/schema"

	_ "modernc.org/sqlite"
)

// DedupWindow is how long an unchanged sample is suppressed. Past it we record
// again even without movement, so flat lines are still visible over time.
const DedupWindow = 30 * time.Minute

// pctEpsilon is the remaining-% delta still treated as "unchanged".
const pctEpsilon = 0.01

const schemaSQL = `
CREATE TABLE IF NOT EXISTS quota_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  recorded_at TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  account TEXT NOT NULL DEFAULT '',
  group_id TEXT NOT NULL,
  group_name TEXT NOT NULL,
  window_id TEXT NOT NULL,
  remaining_pct REAL NOT NULL,
  used_pct REAL NOT NULL,
  resets_in_minutes INTEGER,
  pace_remaining_pct REAL
);
CREATE INDEX IF NOT EXISTS idx_quota_samples_lookup
  ON quota_samples (provider_id, group_id, window_id, recorded_at);`

// Sample is one stored quota reading.
type Sample struct {
	ID               int64    `json:"id"`
	RecordedAt       string   `json:"recordedAt"`
	ProviderID       string   `json:"providerId"`
	Account          string   `json:"account,omitempty"`
	GroupID          string   `json:"groupId"`
	GroupName        string   `json:"groupName"`
	WindowID         string   `json:"windowId"`
	RemainingPct     float64  `json:"remainingPct"`
	UsedPct          float64  `json:"usedPct"`
	ResetsInMinutes  *int     `json:"resetsInMinutes,omitempty"`
	PaceRemainingPct *float64 `json:"paceRemainingPct,omitempty"`
}

// DefaultPath returns the per-user database path:
// ~/Library/Application Support/agentbar/history.db on macOS,
// $XDG_DATA_HOME/agentbar/history.db elsewhere (falling back to
// ~/.local/share/agentbar/history.db).
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "agentbar", "history.db"), nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "agentbar", "history.db"), nil
	}
	return filepath.Join(home, ".local", "share", "agentbar", "history.db"), nil
}

// Store is an open history database.
type Store struct {
	db   *sql.DB
	Path string
}

// Open opens (creating if needed) the history database at path. An empty path
// uses DefaultPath.
func Open(path string) (*Store, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection keeps the PRAGMAs (WAL, busy timeout) on every query and
	// avoids SQLITE_BUSY between the app and a manual CLI query.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=3000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, Path: path}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Record inserts every window of every provider reporting status ok, skipping
// readings identical to the last stored one within DedupWindow. It returns the
// number of rows inserted.
func (s *Store) Record(snap schema.Snapshot) (int, error) {
	at := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339, snap.UpdatedAt); err == nil {
		at = t.UTC()
	}
	stamp := at.Format(time.RFC3339)

	inserted := 0
	for _, p := range snap.Providers {
		if p.Status != schema.StatusOK {
			continue
		}
		for _, g := range p.Groups {
			for _, w := range g.Windows {
				keep, err := s.shouldInsert(p.ID, g.ID, w.ID, w.RemainingPct, at)
				if err != nil {
					return inserted, err
				}
				if !keep {
					continue
				}
				if _, err := s.db.Exec(`INSERT INTO quota_samples
					(recorded_at, provider_id, account, group_id, group_name, window_id,
					 remaining_pct, used_pct, resets_in_minutes, pace_remaining_pct)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					stamp, p.ID, p.Account, g.ID, g.Name, w.ID,
					w.RemainingPct, w.UsedPct, w.ResetsInMinutes, w.PaceRemainingPct,
				); err != nil {
					return inserted, err
				}
				inserted++
			}
		}
	}
	return inserted, nil
}

// shouldInsert reports whether a reading differs enough from the last stored
// sample for that (provider, group, window) triple.
func (s *Store) shouldInsert(providerID, groupID, windowID string, remaining float64, at time.Time) (bool, error) {
	var (
		lastPct  float64
		lastSeen string
	)
	err := s.db.QueryRow(`SELECT remaining_pct, recorded_at FROM quota_samples
		WHERE provider_id = ? AND group_id = ? AND window_id = ?
		ORDER BY recorded_at DESC, id DESC LIMIT 1`,
		providerID, groupID, windowID).Scan(&lastPct, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	lastAt, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		// Unparseable timestamp: record rather than silently drop data.
		return true, nil
	}
	if math.Abs(lastPct-remaining) < pctEpsilon && at.Sub(lastAt) < DedupWindow {
		return false, nil
	}
	return true, nil
}

// QueryRecent returns samples from the last `since` window ordered oldest
// first. An empty providerID returns every provider.
func (s *Store) QueryRecent(since time.Duration, providerID string) ([]Sample, error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)

	query := `SELECT id, recorded_at, provider_id, account, group_id, group_name,
		window_id, remaining_pct, used_pct, resets_in_minutes, pace_remaining_pct
		FROM quota_samples WHERE recorded_at >= ?`
	args := []any{cutoff}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	query += " ORDER BY recorded_at ASC, id ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	samples := []Sample{}
	for rows.Next() {
		var (
			sample   Sample
			resetsIn sql.NullInt64
			paceLeft sql.NullFloat64
		)
		if err := rows.Scan(&sample.ID, &sample.RecordedAt, &sample.ProviderID,
			&sample.Account, &sample.GroupID, &sample.GroupName, &sample.WindowID,
			&sample.RemainingPct, &sample.UsedPct, &resetsIn, &paceLeft); err != nil {
			return nil, err
		}
		if resetsIn.Valid {
			v := int(resetsIn.Int64)
			sample.ResetsInMinutes = &v
		}
		if paceLeft.Valid {
			v := paceLeft.Float64
			sample.PaceRemainingPct = &v
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}
