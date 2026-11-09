package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReplayCursor resumes an oldest-first sweep without rescanning retained rows.
// Reuse it only with the same fixed policy. Restart from nil for a new sweep;
// skipped locked rows and late backdated inserts are reconsidered then.
type ReplayCursor struct {
	ObservedAt time.Time
	StreamID   int64
	Version    int
}

// ReplayPruneBatch reports committed work. Scanned and deleted are at most limit.
type ReplayPruneBatch struct {
	Scanned int
	Deleted int
	Next    *ReplayCursor
}

const replayPruneCandidates = `WITH candidates AS MATERIALIZED (
 SELECT stream_id,observed_at,version FROM public.score_snapshots
 WHERE observed_at<$4 AND (observed_at,stream_id,version)>($5,$6,$7)
 ORDER BY observed_at,stream_id,version LIMIT $8 FOR UPDATE SKIP LOCKED
), deleted AS (
 DELETE FROM public.score_snapshots s USING candidates r
 WHERE s.stream_id=r.stream_id AND s.observed_at=r.observed_at AND s.version=r.version
 AND (` + replayDisposition + `)<>'retained'
 RETURNING s.stream_id,s.observed_at,s.version
)
SELECT r.observed_at,r.stream_id,r.version,d.stream_id IS NOT NULL
FROM candidates r LEFT JOIN deleted d USING(stream_id,observed_at,version)
ORDER BY r.observed_at,r.stream_id,r.version`

// PruneReplayBatch commits one bounded sweep step. It never reads payload JSON,
// changes current scores, or deletes the latest eligible replay within retention.
func (s *Store) PruneReplayBatch(ctx context.Context, p ReplayRetentionPolicy, after *ReplayCursor, limit int) (ReplayPruneBatch, error) {
	var out ReplayPruneBatch
	if err := p.Validate(); err != nil {
		return out, err
	}
	if limit < 1 || limit > 1000 {
		return out, fmt.Errorf("replay batch limit must be between 1 and 1000")
	}
	if p.Retention == 0 {
		return out, nil
	}
	detail, expiry := p.cutoffs()
	before := expiry
	if detail != nil {
		before = detail
	}
	// PostgreSQL negative infinity includes all finite timestamps in the first sweep.
	lower := any(pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true})
	var stream int64
	var version int
	if after != nil {
		lower = after.ObservedAt
		stream = after.StreamID
		version = after.Version
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() // Best-effort cleanup after commit or cancellation.
	rows, err := tx.Query(ctx, replayPruneCandidates, p.AsOf.UTC(), detail, expiry, before, lower, stream, version, limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var cursor ReplayCursor
		var deleted bool
		if err := rows.Scan(&cursor.ObservedAt, &cursor.StreamID, &cursor.Version, &deleted); err != nil {
			rows.Close()
			return ReplayPruneBatch{}, err
		}
		out.Scanned++
		if deleted {
			out.Deleted++
		}
		out.Next = &cursor
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ReplayPruneBatch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReplayPruneBatch{}, err
	}
	return out, nil
}
