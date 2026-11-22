// Package schema defines the normalized multi-provider quota snapshot format.
// Providers (Antigravity today, Claude Code / Codex later) all emit this shape
// so renderers never need provider-specific parsing.
package schema

import (
	"math"
	"time"
)

const (
	StatusOK          = "ok"
	StatusNotSignedIn = "not_signed_in"
	StatusError       = "error"

	WeeklyWindowMinutes   = 7 * 24 * 60
	FiveHourWindowMinutes = 5 * 60
)

type Snapshot struct {
	UpdatedAt string     `json:"updatedAt"`
	Providers []Provider `json:"providers"`
}

type Provider struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Account string       `json:"account"`
	Status  string       `json:"status"`
	Error   string       `json:"error,omitempty"`
	Groups  []ModelGroup `json:"groups"`
}

type ModelGroup struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Windows     []QuotaWindow `json:"windows"`
}

type QuotaWindow struct {
	ID              string  `json:"id"`
	Label           string  `json:"label"`
	RawLabel        string  `json:"rawLabel"`
	RemainingPct    float64 `json:"remainingPct"`
	UsedPct         float64 `json:"usedPct"`
	StatusText      string  `json:"statusText"`
	ResetsInMinutes *int    `json:"resetsInMinutes"`
	WindowMinutes   int     `json:"windowMinutes"`
	// PaceRemainingPct is the remaining % a linear spend would leave right now.
	PaceRemainingPct *float64 `json:"paceRemainingPct"`
}

// NewSnapshot stamps the collection time.
func NewSnapshot() Snapshot {
	return Snapshot{UpdatedAt: time.Now().UTC().Format(time.RFC3339), Providers: []Provider{}}
}

// Finalize fills derived fields (used %, pace marker) for every window.
// Raw parsing only sets remaining % and reset minutes.
func (s *Snapshot) Finalize() {
	for pi := range s.Providers {
		for gi := range s.Providers[pi].Groups {
			for wi := range s.Providers[pi].Groups[gi].Windows {
				w := &s.Providers[pi].Groups[gi].Windows[wi]
				w.UsedPct = round2(100 - w.RemainingPct)
				w.PaceRemainingPct = nil
				if w.ResetsInMinutes != nil && w.WindowMinutes > 0 {
					pace := round2(float64(*w.ResetsInMinutes) / float64(w.WindowMinutes) * 100)
					w.PaceRemainingPct = &pace
				}
			}
		}
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
