// Package cache stores the last snapshot so the menubar can render instantly
// and Waybar polling doesn't spawn agy every second.
package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"agentbar/collector/internal/schema"
)

const DefaultTTL = 5 * time.Minute

type Cache struct {
	Path string
	TTL  time.Duration
}

// New returns a cache at <user cache dir>/agentbar/snapshot.json.
func New(ttl time.Duration) (*Cache, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{Path: filepath.Join(dir, "agentbar", "snapshot.json"), TTL: ttl}, nil
}

// Load returns the snapshot when it exists and is younger than TTL.
func (c *Cache) Load() (*schema.Snapshot, bool) {
	snap, age, err := c.load()
	if err != nil || age > c.TTL {
		return nil, false
	}
	return snap, true
}

// LoadAny returns the snapshot regardless of age (stale-while-error).
func (c *Cache) LoadAny() (*schema.Snapshot, bool) {
	snap, _, err := c.load()
	return snap, err == nil
}

func (c *Cache) load() (*schema.Snapshot, time.Duration, error) {
	st, err := os.Stat(c.Path)
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, 0, err
	}
	var snap schema.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, 0, err
	}
	return &snap, time.Since(st.ModTime()), nil
}

// Save writes atomically (temp file + rename) so readers never see half JSON.
func (c *Cache) Save(snap *schema.Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), "snapshot-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, c.Path); err != nil {
		return err
	}
	return nil
}

// ErrEmpty means no snapshot has ever been written.
var ErrEmpty = errors.New("no cached snapshot")
