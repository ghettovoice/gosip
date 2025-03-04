package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/syncutil"
)

// StatsID represents a statistics identifier.
type StatsID string

// StatsType represents a statistics type.
type StatsType string

// Stats is an interface for statistics implementations.
type Stats = any

type StatsReport = map[StatsID]Stats

type StatsRecorder interface {
	RecordStats(ctx context.Context, id StatsID, stats Stats) error
}

type StdStatsRecorder struct {
	stats syncutil.RWMap[StatsID, Stats]
}

var defStatsRecoder = &StdStatsRecorder{}

func DefaultStatsRecorder() *StdStatsRecorder { return defStatsRecoder }

func (r *StdStatsRecorder) RecordStats(ctx context.Context, id StatsID, stats Stats) error {
	r.stats.Set(id, stats)
	return nil
}
