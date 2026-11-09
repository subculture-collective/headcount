package store

import (
	"context"
	"fmt"
	"time"
)

// ReplayRetentionPolicy evaluates the archive at a fixed time. Zero Retention
// disables pruning; zero Detail disables thinning but preserves outer expiry.
type ReplayRetentionPolicy struct {
	AsOf      time.Time
	Detail    time.Duration
	Retention time.Duration
}

// Validate rejects ambiguous times and negative or inverted retention windows.
func (p ReplayRetentionPolicy) Validate() error {
	if p.AsOf.IsZero() {
		return fmt.Errorf("a fixed as-of time is required")
	}
	if p.Detail < 0 || p.Retention < 0 {
		return fmt.Errorf("retention windows cannot be negative")
	}
	if p.Retention > 0 && p.Detail > p.Retention {
		return fmt.Errorf("detail window cannot exceed the outer retention window")
	}
	return nil
}

func (p ReplayRetentionPolicy) cutoffs() (detail, expiry *time.Time) {
	if p.Retention > 0 {
		t := p.AsOf.UTC().Add(-p.Retention)
		expiry = &t
		if p.Detail > 0 {
			t := p.AsOf.UTC().Add(-p.Detail)
			detail = &t
		}
	}
	return detail, expiry
}

// replayRetentionRows is the shared selection contract for previews and a future
// bounded pruner. It returns identities, payload sizes and disjoint dispositions.
// Rows after as-of are preserved and cannot displace the newest as-of evidence.
// replayDisposition is shared by the aggregate preview and bounded deletion.
// An as-of newer row makes an older row redundant; future rows cannot do so.
const replayDisposition = `CASE WHEN $3::timestamptz IS NULL THEN 'retained'
 WHEN r.observed_at<$3 THEN 'expired'
 WHEN $2::timestamptz IS NULL OR r.observed_at>=$2 OR r.observed_at>$1 THEN 'retained'
 WHEN EXISTS(SELECT 1 FROM public.score_snapshots newer
   WHERE newer.stream_id=r.stream_id AND newer.version=r.version
   AND newer.observed_at>r.observed_at AND newer.observed_at<=$1) THEN 'thinnable'
 ELSE 'retained' END`

const replayRetentionRows = `WITH retention_rows AS (
 SELECT r.stream_id,r.observed_at,r.version,
 pg_column_size(r.input)::bigint+pg_column_size(r.output)::bigint AS payload_bytes,
 ` + replayDisposition + ` AS disposition FROM public.score_snapshots r
) `

// ReplayRetentionTotals describes referenced JSON values, excluding indexes,
// relation overhead and already-dead versions. Payload sizes reflect compression.
type ReplayRetentionTotals struct {
	Snapshots    int64 `json:"snapshots"`
	PayloadBytes int64 `json:"payload_bytes"`
}

// ReplayStorageAllocation separates physical allocation from estimated row churn.
// MainTableBytes includes the main relation's auxiliary forks, excluding indexes.
// TOASTBytes includes the TOAST relation and its indexes.
type ReplayStorageAllocation struct {
	MainTableBytes        int64      `json:"main_table_bytes"`
	MainIndexBytes        int64      `json:"main_index_bytes"`
	TOASTBytes            int64      `json:"toast_bytes"`
	TotalAllocatedBytes   int64      `json:"total_allocated_bytes"`
	LiveRowsEstimate      int64      `json:"live_rows_estimate"`
	DeadRowsEstimate      int64      `json:"dead_rows_estimate"`
	TOASTDeadRowsEstimate int64      `json:"toast_dead_rows_estimate"`
	LastAutovacuum        *time.Time `json:"last_autovacuum"`
	StatisticsReset       *time.Time `json:"statistics_reset"`
}

// ReplayRetentionPreview is an aggregate-only report; it contains no replay data.
type ReplayRetentionPreview struct {
	Mode           string                  `json:"mode"`
	AsOf           time.Time               `json:"as_of"`
	MeasuredAt     time.Time               `json:"measured_at"`
	DetailWindow   string                  `json:"detail_window"`
	OuterWindow    string                  `json:"outer_window"`
	PruningEnabled bool                    `json:"policy_pruning_enabled"`
	DetailCutoff   *time.Time              `json:"detail_cutoff"`
	ExpiryCutoff   *time.Time              `json:"expiry_cutoff"`
	StreamVersions int64                   `json:"stream_versions"`
	FutureRows     int64                   `json:"rows_after_as_of"`
	Total          ReplayRetentionTotals   `json:"total"`
	Retained       ReplayRetentionTotals   `json:"retained"`
	Expired        ReplayRetentionTotals   `json:"expired"`
	Thinnable      ReplayRetentionTotals   `json:"thinnable"`
	Storage        ReplayStorageAllocation `json:"storage"`
	Note           string                  `json:"note"`
}

// PreviewReplayRetention reads a consistent, read-only view of the archive.
// This method never migrates schema, deletes rows or changes retention settings.
func (s *Store) PreviewReplayRetention(ctx context.Context, p ReplayRetentionPolicy) (ReplayRetentionPreview, error) {
	var out ReplayRetentionPreview
	if err := p.Validate(); err != nil {
		return out, err
	}
	detail, expiry := p.cutoffs()
	out.Mode = "read_only_preview"
	out.AsOf, out.DetailCutoff, out.ExpiryCutoff = p.AsOf.UTC(), detail, expiry
	out.DetailWindow, out.OuterWindow = p.Detail.String(), p.Retention.String()
	out.PruningEnabled = p.Retention > 0
	out.Note = "Referenced payload bytes are not filesystem savings. Allocation and row estimates are live diagnostics, not frozen MVCC data. Rows after as-of are preserved and do not displace the latest as-of replay."
	err := s.ReadSnapshot(ctx, func(view *Store, measuredAt time.Time) error {
		out.MeasuredAt = measuredAt
		if err := view.reader().QueryRow(ctx, replayRetentionRows+`
 SELECT count(*),coalesce(sum(payload_bytes),0),
        count(*) FILTER(WHERE disposition='retained'),coalesce(sum(payload_bytes) FILTER(WHERE disposition='retained'),0),
        count(*) FILTER(WHERE disposition='expired'),coalesce(sum(payload_bytes) FILTER(WHERE disposition='expired'),0),
        count(*) FILTER(WHERE disposition='thinnable'),coalesce(sum(payload_bytes) FILTER(WHERE disposition='thinnable'),0),
        count(DISTINCT (stream_id,version)),count(*) FILTER(WHERE observed_at>$1)
 FROM retention_rows`, out.AsOf, detail, expiry).Scan(
			&out.Total.Snapshots, &out.Total.PayloadBytes,
			&out.Retained.Snapshots, &out.Retained.PayloadBytes,
			&out.Expired.Snapshots, &out.Expired.PayloadBytes,
			&out.Thinnable.Snapshots, &out.Thinnable.PayloadBytes,
			&out.StreamVersions, &out.FutureRows); err != nil {
			return fmt.Errorf("preview replay selection: %w", err)
		}
		return view.reader().QueryRow(ctx, `
 SELECT pg_table_size(c.oid)-pg_total_relation_size(c.reltoastrelid),pg_indexes_size(c.oid),
        pg_total_relation_size(c.reltoastrelid),pg_total_relation_size(c.oid),
        coalesce(s.n_live_tup,0),coalesce(s.n_dead_tup,0),coalesce(t.n_dead_tup,0),
        s.last_autovacuum,d.stats_reset
 FROM pg_class c
 LEFT JOIN pg_stat_all_tables s ON s.relid=c.oid
 LEFT JOIN pg_stat_all_tables t ON t.relid=c.reltoastrelid
 LEFT JOIN pg_stat_database d ON d.datname=current_database()
 WHERE c.oid='public.score_snapshots'::regclass`).Scan(
			&out.Storage.MainTableBytes, &out.Storage.MainIndexBytes, &out.Storage.TOASTBytes,
			&out.Storage.TotalAllocatedBytes, &out.Storage.LiveRowsEstimate, &out.Storage.DeadRowsEstimate,
			&out.Storage.TOASTDeadRowsEstimate, &out.Storage.LastAutovacuum, &out.Storage.StatisticsReset)
	})
	if err != nil {
		return ReplayRetentionPreview{}, fmt.Errorf("preview replay retention: %w", err)
	}
	return out, nil
}
