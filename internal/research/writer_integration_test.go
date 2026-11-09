package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/score"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
	"github.com/jackc/pgx/v5"
)

func writerStore(t *testing.T) *store.Store {
	t.Helper()
	raw := os.Getenv("HEADCOUNT_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("HEADCOUNT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("headcount_writer_%d", time.Now().UnixNano())
	if _, err := admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	s, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func writerPacket(t *testing.T, s *store.Store, id int64) Packet {
	t.Helper()
	_, p := hydrationFixture(t, false, score.Version)
	p.Input.Ref.ID = id
	p.Input.Behavior = map[string]float64{"p95_messages": 2}
	p.Output = score.Evaluate(p.Input, nil)
	var err error
	p.Digest, err = Digest(p.Input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(context.Background(), `INSERT INTO channels(id,login,display_name,first_seen_at) VALUES(1,'fixture','Fixture',$1) ON CONFLICT DO NOTHING`, p.Input.Ref.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(context.Background(), `INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at,ended_at,language) VALUES($2,1,$1,$1,$3,$3,'en')`, p.Input.Ref.StartedAt, id, p.Input.Ref.LastSeenAt)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func packetScore(p Packet) store.Score {
	return store.Score{StreamID: p.Input.Ref.ID, ChannelID: p.Input.Ref.ChannelID, LastSeenAt: p.Input.Ref.LastSeenAt, Version: p.Output.Version, Minutes: len(p.Input.Series.Viewers), Quotient: p.Output.Quotient, Confidence: p.Output.Coverage, Final: true, Components: p.Output.Components, Evidence: p.Output.Evidence, SnapshotInput: p.Input, SnapshotOutput: p.Output, ObservedAt: p.Input.ObservedAt, Peer: p.Output.Peer, Behavior: p.Input.Behavior}
}

func saveReferenced(t *testing.T, s *store.Store, p Packet) store.ReplayWriteStats {
	t.Helper()
	write, err := score.EncodeReplay(p.Input)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s.SaveReferencedScore(context.Background(), packetScore(p), write)
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func storedPacket(t *testing.T, s *store.Store, p Packet) (store.StoredReplay, Packet) {
	t.Helper()
	raw, err := s.ReplaySnapshot(context.Background(), p.Input.Ref.ID, &p.Input.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := HydrateReplay(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("stored format changed complete packet, identity, digest or output")
	}
	return raw, got
}

func writerCount(t *testing.T, s *store.Store, table string) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReferencedWriterInlineParityAndLegacyRetryIntegration(t *testing.T) {
	s := writerStore(t)
	ctx := context.Background()
	p := writerPacket(t, s, 10)
	if err := s.SaveScore(ctx, packetScore(p)); err != nil {
		t.Fatal(err)
	}
	raw, _ := storedPacket(t, s, p)
	if raw.StorageVersion != 1 || raw.InputDigest != nil {
		t.Fatal("default writer changed legacy storage")
	}
	stats := saveReferenced(t, s, p)
	if stats.SnapshotInserted || stats.NewContexts != 0 || writerCount(t, s, "replay_contexts") != 0 {
		t.Fatal("retry rewrote a legacy row or created unused contexts")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM score_snapshots`); err != nil {
		t.Fatal(err)
	}
	stats = saveReferenced(t, s, p)
	if stats.StorageVersion != 2 || !stats.SnapshotInserted || stats.NewContexts != 2 || stats.ReusedContexts != 0 || stats.NewContextBytes <= 0 || stats.SnapshotPayloadBytes <= 0 {
		t.Fatalf("unexpected committed stats: %+v", stats)
	}
	_, hydrated := storedPacket(t, s, p)
	want, err := json.Marshal(score.Evaluate(p.Input, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(score.Evaluate(hydrated.Input, nil))
	if err != nil || !bytes.Equal(want, got) {
		t.Fatal("referenced representation changed evaluation")
	}
	if err := s.SaveScore(ctx, packetScore(p)); err != nil {
		t.Fatalf("inline-mode retry of referenced identity: %v", err)
	}
	stats = saveReferenced(t, s, p)
	if stats.SnapshotInserted || stats.NewContexts != 0 || stats.NewContextBytes != 0 || stats.SnapshotPayloadBytes != 0 {
		t.Fatal("idempotent retry reported new stored bytes")
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE stream_peer_metrics SET median_viewers=999999; UPDATE stream_behavior_metrics SET metrics='{}'`); err != nil {
		t.Fatal(err)
	}
	storedPacket(t, s, p)
}

func TestReferencedWriterConcurrentReuseAndChangedContextIntegration(t *testing.T) {
	s := writerStore(t)
	const n = 8
	packets := make([]Packet, n)
	for i := range n {
		packets[i] = writerPacket(t, s, int64(100+i))
	}
	start := make(chan struct{})
	errors := make(chan error, n)
	results := make(chan store.ReplayWriteStats, n)
	var wg sync.WaitGroup
	for _, p := range packets {
		wg.Go(func() {
			<-start
			write, err := score.EncodeReplay(p.Input)
			if err != nil {
				errors <- err
				return
			}
			stats, err := s.SaveReferencedScore(context.Background(), packetScore(p), write)
			if err != nil {
				errors <- err
				return
			}
			results <- stats
		})
	}
	close(start)
	wg.Wait()
	close(errors)
	close(results)
	for err := range errors {
		t.Fatal(err)
	}
	var created, reused int
	for stats := range results {
		created += stats.NewContexts
		reused += stats.ReusedContexts
	}
	if created != 2 || reused != 2*(n-1) || writerCount(t, s, "replay_contexts") != 2 || writerCount(t, s, "score_snapshot_contexts") != 2*n {
		t.Fatalf("concurrent identity reuse failed: new=%d reused=%d", created, reused)
	}
	for _, p := range packets {
		storedPacket(t, s, p)
	}
	changed := writerPacket(t, s, 200)
	changed.Input.Peers[0], changed.Input.Peers[1] = changed.Input.Peers[1], changed.Input.Peers[0]
	changed.Output = score.Evaluate(changed.Input, nil)
	var err error
	changed.Digest, err = Digest(changed.Input)
	if err != nil {
		t.Fatal(err)
	}
	stats := saveReferenced(t, s, changed)
	if stats.NewContexts != 1 || stats.ReusedContexts != 1 || writerCount(t, s, "replay_contexts") != 3 {
		t.Fatal("changed peer order reused an incompatible immutable object")
	}
	storedPacket(t, s, changed)
	storedPacket(t, s, packets[0])
}

func TestReferencedWriterCollisionAndLateFailureRollbackIntegration(t *testing.T) {
	s := writerStore(t)
	ctx := context.Background()
	p := writerPacket(t, s, 10)
	saveReferenced(t, s, p)
	var before []byte
	if err := s.Pool.QueryRow(ctx, `SELECT row_to_json(s) FROM stream_scores s WHERE stream_id=10`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, outputChange := range []bool{false, true} {
		var altered Packet
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &altered); err != nil {
			t.Fatal(err)
		}
		if outputChange {
			altered.Output.Quotient++
		} else {
			altered.Input.Peers[0].MedianViewers++
		}
		write, err := score.EncodeReplay(altered.Input)
		if err != nil {
			t.Fatal(err)
		}
		if stats, err := s.SaveReferencedScore(ctx, packetScore(altered), write); err == nil || stats != (store.ReplayWriteStats{}) {
			t.Fatal("collision succeeded or reported uncommitted counters")
		}
		var after []byte
		if err := s.Pool.QueryRow(ctx, `SELECT row_to_json(s) FROM stream_scores s WHERE stream_id=10`).Scan(&after); err != nil || !bytes.Equal(before, after) {
			t.Fatal("collision changed current score")
		}
		if writerCount(t, s, "replay_contexts") != 2 || writerCount(t, s, "score_snapshots") != 1 {
			t.Fatal("collision published partial evidence")
		}
		storedPacket(t, s, p)
	}
	if _, err := s.Pool.Exec(ctx, `CREATE FUNCTION fail_current_score() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected current-score failure'; END $$;
 CREATE TRIGGER writer_late_failure BEFORE INSERT ON stream_scores FOR EACH ROW EXECUTE FUNCTION fail_current_score()`); err != nil {
		t.Fatal(err)
	}
	other := writerPacket(t, s, 20)
	other.Input.Peers[0].MedianViewers++
	write, err := score.EncodeReplay(other.Input)
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := s.SaveReferencedScore(ctx, packetScore(other), write); err == nil || stats != (store.ReplayWriteStats{}) {
		t.Fatal("late failure committed or reported counters")
	}
	for _, table := range []string{"score_snapshots", "stream_scores", "stream_peer_metrics", "stream_behavior_metrics"} {
		if writerCount(t, s, table) != 1 {
			t.Fatal("late failure left partial score/cache/snapshot state: " + table)
		}
	}
	if writerCount(t, s, "replay_contexts") != 2 || writerCount(t, s, "score_snapshot_contexts") != 2 {
		t.Fatal("late failure left new contexts or bindings")
	}
}

func TestReferencedWriterRetriesContextForeignKeyRaceIntegration(t *testing.T) {
	s := writerStore(t)
	ctx := context.Background()
	p := writerPacket(t, s, 10)
	p.Input.BehaviorPeers = nil
	p.Output = score.Evaluate(p.Input, nil)
	// Fault injection exercises the FK retry path after all prior score/cache
	// writes. Sequence increments survive rollback, so only the first attempt fails.
	if _, err := s.Pool.Exec(ctx, `CREATE SEQUENCE writer_ref_attempt;
 CREATE FUNCTION expire_first_writer_context() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF nextval('writer_ref_attempt')=1 THEN DELETE FROM replay_contexts WHERE digest=NEW.context_digest; END IF;
 RETURN NEW; END $$;
 CREATE TRIGGER writer_ref_race BEFORE INSERT ON score_snapshot_contexts FOR EACH ROW EXECUTE FUNCTION expire_first_writer_context()`); err != nil {
		t.Fatal(err)
	}
	stats := saveReferenced(t, s, p)
	var attempts int
	if err := s.Pool.QueryRow(ctx, `SELECT last_value FROM writer_ref_attempt`).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("whole transaction was not retried once: %d %v", attempts, err)
	}
	if stats.NewContexts != 1 || stats.ReusedContexts != 0 || writerCount(t, s, "replay_contexts") != 1 || writerCount(t, s, "score_snapshots") != 1 {
		t.Fatal("failed-attempt counters or evidence escaped rollback")
	}
	var err error
	p.Digest, err = Digest(p.Input)
	if err != nil {
		t.Fatal(err)
	}
	storedPacket(t, s, p)
}

func TestReferencedWriterRetriesGCWinningBeforeKeyShareIntegration(t *testing.T) {
	s := writerStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := writerPacket(t, s, 10)
	p.Input.BehaviorPeers = nil
	p.Output = score.Evaluate(p.Input, nil)
	var err error
	p.Digest, err = Digest(p.Input)
	if err != nil {
		t.Fatal(err)
	}
	write, err := score.EncodeReplay(p.Input)
	if err != nil || len(write.Contexts) != 1 {
		t.Fatal("one-context race fixture unavailable")
	}
	c := write.Contexts[0]
	seed, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seed.Rollback(context.Background()) }() // Best effort after commit/failure.
	if err := store.PutReplayContext(ctx, seed, c); err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	gc, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gc.Rollback(context.Background()) }() // Best effort after commit/failure.
	if _, err := gc.Exec(ctx, `SELECT digest FROM replay_contexts WHERE digest=$1 FOR UPDATE`, c.Digest); err != nil {
		t.Fatal(err)
	}
	type saved struct {
		stats store.ReplayWriteStats
		err   error
	}
	result := make(chan saved, 1)
	go func() {
		stats, err := s.SaveReferencedScore(ctx, packetScore(p), write)
		result <- saved{stats, err}
	}()
	for {
		var waiting bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%FOR KEY SHARE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The writer already observed an existing digest, but GC owns its update
	// lock. Deleting/committing now makes that key-share SELECT return no rows.
	if _, err := gc.Exec(ctx, `DELETE FROM replay_contexts WHERE digest=$1`, c.Digest); err != nil {
		t.Fatal(err)
	}
	if err := gc.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || got.stats.NewContexts != 1 || got.stats.ReusedContexts != 0 {
		t.Fatalf("writer failed to retry full transaction after GC: %+v %v", got.stats, got.err)
	}
	if writerCount(t, s, "score_snapshots") != 1 || writerCount(t, s, "replay_contexts") != 1 || writerCount(t, s, "score_snapshot_contexts") != 1 {
		t.Fatal("GC retry left partial or missing evidence")
	}
	storedPacket(t, s, p)
}

func TestReferencedWriterKeepsEmptyArraysInlineIntegration(t *testing.T) {
	s := writerStore(t)
	for i, peers := range [][]store.PeerStat{nil, {}} {
		p := writerPacket(t, s, int64(10+i))
		p.Input.Peers, p.Input.BehaviorPeers = peers, nil
		p.Output = score.Evaluate(p.Input, nil)
		var err error
		p.Digest, err = Digest(p.Input)
		if err != nil {
			t.Fatal(err)
		}
		stats := saveReferenced(t, s, p)
		raw, _ := storedPacket(t, s, p)
		if stats.StorageVersion != 1 || stats.NewContexts != 0 || stats.ReusedContexts != 0 || raw.StorageVersion != 1 || raw.InputDigest != nil || len(raw.Contexts) != 0 {
			t.Fatal("empty/null arrays created unnecessary objects or referenced format")
		}
	}
}

func TestReferencedWriterVersionsAndNormalExpiryIntegration(t *testing.T) {
	s := writerStore(t)
	ctx := context.Background()
	p := writerPacket(t, s, 10)
	saveReferenced(t, s, p)
	other := p
	other.Output.Version++
	stats := saveReferenced(t, s, other)
	if stats.NewContexts != 0 || stats.ReusedContexts != 2 || writerCount(t, s, "score_snapshots") != 2 {
		t.Fatal("model version changed a retained identity or failed to reuse frozen context")
	}
	if err := s.SaveScore(ctx, packetScore(p)); err != nil {
		t.Fatal("retry selected another model's observation at the same timestamp")
	}
	storedPacket(t, s, other)
	// A newer observation protects the shared contexts after the first two
	// model-version identities expire, then the last-reference expiry permits GC.
	other.Input.ObservedAt = other.Input.ObservedAt.Add(8 * 24 * time.Hour)
	var err error
	other.Digest, err = Digest(other.Input)
	if err != nil {
		t.Fatal(err)
	}
	saveReferenced(t, s, other)
	_, portable := storedPacket(t, s, other)
	policy := store.ReplayRetentionPolicy{AsOf: other.Input.ObservedAt, Detail: 48 * time.Hour, Retention: 7 * 24 * time.Hour}
	batch, err := s.PruneReplayBatch(ctx, policy, nil, 100)
	if err != nil || batch.Deleted != 2 {
		t.Fatalf("old model evidence did not expire normally: %+v %v", batch, err)
	}
	gc, err := s.PruneReplayContexts(ctx, "", 100)
	if err != nil || gc.Deleted != 0 {
		t.Fatal("expiry of another snapshot removed shared context")
	}
	storedPacket(t, s, other)
	policy.AsOf = policy.AsOf.Add(8 * 24 * time.Hour)
	batch, err = s.PruneReplayBatch(ctx, policy, nil, 100)
	if err != nil || batch.Deleted != 1 {
		t.Fatal("last snapshot did not expire")
	}
	gc, err = s.PruneReplayContexts(ctx, "", 100)
	if err != nil || gc.Deleted != 2 || writerCount(t, s, "replay_contexts") != 0 {
		t.Fatal("unreferenced contexts did not expire")
	}
	if err := portable.Verify(); err != nil {
		t.Fatal("expiry invalidated the portable packet")
	}
}
