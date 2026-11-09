package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReplayPruneIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	p := ReplayRetentionPolicy{at, 48 * time.Hour, 7 * 24 * time.Hour}
	_, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots
 SELECT stream, $1::timestamptz-make_interval(days=>age),version,'{}','{}'
 FROM generate_series(1,3) stream CROSS JOIN generate_series(2,8) age CROSS JOIN (VALUES(9),(11)) v(version);
 `, at)
	if err != nil {
		t.Fatal(err)
	}
	// Future observations must not displace the protected old latest.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots VALUES(7,$1::timestamptz-interval '6 days',11,'{}','{}'),(7,$1::timestamptz+interval '1 hour',11,'{}','{}'),(8,$1::timestamptz-interval '8 days',11,'{}','{}')`, at); err != nil {
		t.Fatal(err)
	}
	detail, expiry := p.cutoffs()
	var expected int
	if err := s.Pool.QueryRow(ctx, replayRetentionRows+`SELECT count(*) FROM retention_rows WHERE disposition<>'retained'`, at, detail, expiry).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		policy ReplayRetentionPolicy
		want   int
	}{
		{"disabled", ReplayRetentionPolicy{at, 48 * time.Hour, 0}, 0},
		{"tiered", p, expected},
		{"idempotent", p, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cursor *ReplayCursor
			deleted := 0
			for range 100 {
				b, err := s.PruneReplayBatch(ctx, tc.policy, cursor, 2)
				if err != nil {
					t.Fatal(err)
				}
				if b.Scanned > 2 || b.Deleted > b.Scanned {
					t.Fatalf("unbounded batch: %+v", b)
				}
				deleted += b.Deleted
				cursor = b.Next
				if b.Scanned < 2 {
					break
				}
			}
			if deleted != tc.want {
				t.Fatalf("deleted %d want %d", deleted, tc.want)
			}
		})
	}
	r, err := s.PreviewReplayRetention(ctx, p)
	if err != nil || r.Expired.Snapshots != 0 || r.Thinnable.Snapshots != 0 || r.Total.Snapshots != 8 {
		t.Fatalf("retained selection: %+v %v", r, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.PruneReplayBatch(cancelled, p, nil, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, n := range []int{0, 1001} {
		if _, err := s.PruneReplayBatch(ctx, p, nil, n); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
}

func TestReplayPruneLockedAndCancelledIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	p := ReplayRetentionPolicy{at, 0, 7 * 24 * time.Hour}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots VALUES(1,$1::timestamptz-interval '9 days',11,'{}','{}'),(2,$1::timestamptz-interval '8 days',11,'{}','{}')`, at); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // Cleanup also runs after the explicit rollback.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM score_snapshots WHERE stream_id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	b, err := s.PruneReplayBatch(ctx, p, nil, 2)
	if err != nil || b.Scanned != 1 || b.Deleted != 1 {
		t.Fatalf("locked batch: %+v %v", b, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	b, err = s.PruneReplayBatch(ctx, p, nil, 2)
	if err != nil || b.Deleted != 1 {
		t.Fatalf("next sweep skipped locked row: %+v %v", b, err)
	}
	// Cancellation during deletion rolls back the entire batch.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots VALUES(1,$1::timestamptz-interval '9 days',11,'{}','{}');`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `CREATE FUNCTION delay_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(2); RETURN OLD; END $$; CREATE TRIGGER delay_delete BEFORE DELETE ON score_snapshots FOR EACH ROW EXECUTE FUNCTION delay_delete()`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := s.PruneReplayBatch(short, p, nil, 2); err == nil {
		t.Fatal("interrupted deletion committed")
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM score_snapshots`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rollback rows=%d err=%v", n, err)
	}
}

func TestReplayPruneConcurrentScoreIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO channels(id,login,display_name,first_seen_at) VALUES(1,'fixture','Fixture',$1)`, at.Add(-10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at) VALUES(1,1,$1,$1,$2)`, at.Add(-10*24*time.Hour), at); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := range 50 {
			sc := Score{StreamID: 1, ChannelID: 1, LastSeenAt: at, ObservedAt: at.Add(-3*24*time.Hour + time.Duration(i)*time.Second), Version: 11, SnapshotInput: map[string]int{"i": i}, SnapshotOutput: map[string]int{"i": i}}
			if err := s.SaveScore(ctx, sc); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	p := ReplayRetentionPolicy{at, 48 * time.Hour, 7 * 24 * time.Hour}
	for range 30 {
		if _, err := s.PruneReplayBatch(ctx, p, nil, 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var latest time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT max(observed_at) FROM score_snapshots WHERE stream_id=1 AND version=11`).Scan(&latest); err != nil || !latest.Equal(at.Add(-3*24*time.Hour+49*time.Second)) {
		t.Fatalf("newest evidence lost: %v %v", latest, err)
	}
	var scores int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM stream_scores WHERE stream_id=1`).Scan(&scores); err != nil || scores != 1 {
		t.Fatalf("current score changed: %d %v", scores, err)
	}
}

func TestReplayPrunePlanIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots SELECT stream,$1::timestamptz-make_interval(hours=>age),11,'{}','{}' FROM generate_series(1,1000) stream CROSS JOIN generate_series(49,98) age;`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `ANALYZE score_snapshots`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // Cleanup also runs after the explicit rollback.
	p := ReplayRetentionPolicy{at, 48 * time.Hour, 7 * 24 * time.Hour}
	d, e := p.cutoffs()
	rows, err := tx.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS) `+replayPruneCandidates, at, d, e, d, at.Add(-100*24*time.Hour), int64(0), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	t.Log(plan.String())
	if !strings.Contains(plan.String(), "Index Scan") || !strings.Contains(plan.String(), "Limit") {
		t.Fatal("candidate selection lost bounded indexed path")
	}
}
