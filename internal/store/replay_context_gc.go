package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ReplayContextGC reports one committed context sweep step. Cursor traversal
// includes referenced objects so retained rows cannot starve orphan discovery.
type ReplayContextGC struct {
	Scanned      int
	Deleted      int64
	PayloadBytes int64
	NextDigest   string
}

// PruneReplayContexts locks at most limit keys, skipping in-flight writers, then
// rechecks references in a fresh READ COMMITTED statement before deletion. The
// restricting foreign key remains the final guard. Empty after starts a sweep;
// restart there after reaching its end to reconsider skipped/late objects.
func (s *Store) PruneReplayContexts(ctx context.Context, after string, limit int) (ReplayContextGC, error) {
	var out ReplayContextGC
	if limit < 1 || limit > 1000 {
		return out, fmt.Errorf("context batch limit must be between 1 and 1000")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() // Best-effort after commit/cancellation.
	rows, err := tx.Query(ctx, `SELECT digest FROM replay_contexts WHERE digest>$1 ORDER BY digest LIMIT $2 FOR UPDATE SKIP LOCKED`, after, limit)
	if err != nil {
		return out, err
	}
	var keys []string
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			rows.Close()
			return ReplayContextGC{}, err
		}
		keys = append(keys, digest)
		out.NextDigest = digest
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ReplayContextGC{}, err
	}
	out.Scanned = len(keys)
	if len(keys) > 0 {
		err = tx.QueryRow(ctx, `WITH removed AS (
 DELETE FROM replay_contexts c WHERE c.digest=ANY($1::text[])
 AND NOT EXISTS(SELECT 1 FROM score_snapshot_contexts r WHERE r.context_digest=c.digest)
 RETURNING pg_column_size(c.canonical)::bigint AS bytes)
 SELECT count(*),coalesce(sum(bytes),0) FROM removed`, keys).Scan(&out.Deleted, &out.PayloadBytes)
		if err != nil {
			return ReplayContextGC{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ReplayContextGC{}, err
	}
	return out, nil
}
