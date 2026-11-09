package score

import (
	"context"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

// RunReplayRetention runs independently of scoring backfills. Detail defaults to
// zero (outer expiry only); native activation of thinning is a separate release.
func (s *Scorer) RunReplayRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var cursor *store.ReplayCursor
	var contextCursor string
	var policy store.ReplayRetentionPolicy
	var next time.Time
	for {
		if s.ReplayRetention > 0 && !time.Now().Before(next) {
			if policy.AsOf.IsZero() {
				policy = store.ReplayRetentionPolicy{AsOf: time.Now().UTC(), Detail: s.ReplayDetail, Retention: s.ReplayRetention}
			}
			batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			var scanned, deleted int
			for range 100 {
				batch, err := s.store.PruneReplayBatch(batchCtx, policy, cursor, 500)
				if err != nil {
					s.log.Warn("prune replays", "err", err)
					break
				}
				scanned += batch.Scanned
				deleted += batch.Deleted
				if batch.Next != nil {
					cursor = batch.Next
				}
				if batch.Scanned < 500 {
					policy = store.ReplayRetentionPolicy{}
					cursor = nil
					next = time.Now().Add(time.Hour)
					break
				}
			}
			cancel()
			s.log.Info("replay retention", "scanned", scanned, "deleted", deleted)
		}
		if s.ReplayRetention > 0 && ctx.Err() == nil {
			// Separate budget keeps orphan cleanup progressing even when replay
			// pruning uses its whole time slice or waits for its hourly cycle.
			gcCtx, gcCancel := context.WithTimeout(ctx, 5*time.Second)
			var scanned int
			var deleted, bytes int64
			for range 10 {
				batch, err := s.store.PruneReplayContexts(gcCtx, contextCursor, 100)
				if err != nil {
					s.log.Warn("prune replay contexts", "err", err)
					break
				}
				scanned += batch.Scanned
				deleted += batch.Deleted
				bytes += batch.PayloadBytes
				if batch.NextDigest != "" {
					contextCursor = batch.NextDigest
				}
				if batch.Scanned < 100 {
					contextCursor = ""
					break
				}
			}
			gcCancel()
			if scanned > 0 {
				s.log.Info("replay context retention", "scanned", scanned, "deleted", deleted, "payload_bytes", bytes)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
