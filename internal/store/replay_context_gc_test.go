package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func gcContext(t *testing.T, n int) ReplayContext {
	t.Helper()
	c := fixtureContext(t)
	c.Canonical = bytes.ReplaceAll(c.Canonical, []byte(`"StreamID":201`), []byte(`"StreamID":`+strconv.Itoa(201+n)))
	h := sha256.Sum256(append([]byte("headcount.replay-context.v1\x00"), c.Canonical...))
	c.Digest = hex.EncodeToString(h[:])
	return c
}

func TestBoundedContextGCAlongsideReplayExpiryIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	tx := contextTx(t, s)
	contexts := []ReplayContext{}
	for i := range 5 {
		c := gcContext(t, i)
		contexts = append(contexts, c)
		if err := PutReplayContext(ctx, tx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ids := []ReplayIdentity{{1, at.Add(-8 * 24 * time.Hour), 9}, {3, at.Add(-8 * 24 * time.Hour), 9}, {1, at.Add(-time.Hour), 11}, {2, at.Add(-6 * 24 * time.Hour), 9}}
	for i, id := range ids {
		contextFragment(t, s, id)
		tx := contextTx(t, s)
		c := contexts[0]
		if i == 1 {
			c = contexts[1]
		}
		if i == 3 {
			c = contexts[2]
		}
		if err := BindReplayContext(ctx, tx, id, "peers", c.Digest); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	p := ReplayRetentionPolicy{at, 48 * time.Hour, 7 * 24 * time.Hour}
	b, err := s.PruneReplayBatch(ctx, p, nil, 100)
	if err != nil || b.Deleted != 2 {
		t.Fatalf("snapshot pruning: %+v %v", b, err)
	}
	var after string
	var deleted int64
	for range 10 {
		b, err := s.PruneReplayContexts(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if b.Scanned > 2 || b.Deleted > int64(b.Scanned) {
			t.Fatal("unbounded GC work")
		}
		after = b.NextDigest
		deleted += b.Deleted
		if b.Scanned < 2 {
			break
		}
	}
	if deleted != 3 {
		t.Fatalf("removed %d contexts want 3", deleted)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM replay_contexts`).Scan(&n); err != nil || n != 2 {
		t.Fatal("retained contexts lost")
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM score_snapshot_contexts r LEFT JOIN replay_contexts c ON c.digest=r.context_digest WHERE c.digest IS NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatal("dangling retained references")
	}
	b2, err := s.PruneReplayContexts(ctx, "", 100)
	if err != nil || b2.Deleted != 0 {
		t.Fatalf("non-idempotent cleanup: %+v %v", b2, err)
	}
	p.AsOf = at.Add(8 * 24 * time.Hour)
	if _, err := s.PruneReplayBatch(ctx, p, nil, 100); err != nil {
		t.Fatal(err)
	}
	b2, err = s.PruneReplayContexts(ctx, "", 100)
	if err != nil || b2.Deleted != 2 || b2.PayloadBytes <= 0 {
		t.Fatalf("last reference cleanup: %+v %v", b2, err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := s.PruneReplayContexts(ctx, "", limit); err == nil {
			t.Fatal("invalid GC limit")
		}
	}
}

func TestContextGCProtectsInFlightWritersIntegration(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing context", true: "new context"}[create], func(t *testing.T) {
			s := bugTestStore(t)
			ctx := context.Background()
			id := ReplayIdentity{1, time.Now().UTC(), 11}
			contextFragment(t, s, id)
			c := gcContext(t, 1)
			if !create {
				seed := contextTx(t, s)
				if err := PutReplayContext(ctx, seed, c); err != nil {
					t.Fatal(err)
				}
				if err := seed.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			writer := contextTx(t, s)
			if err := PutReplayContext(ctx, writer, c); err != nil {
				t.Fatal(err)
			}
			b, err := s.PruneReplayContexts(ctx, "", 100)
			if err != nil || b.Deleted != 0 {
				t.Fatalf("GC raced live writer: %+v %v", b, err)
			}
			if err := BindReplayContext(ctx, writer, id, "peers", c.Digest); err != nil {
				t.Fatal(err)
			}
			if err := writer.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			b, err = s.PruneReplayContexts(ctx, "", 100)
			if err != nil || b.Deleted != 0 {
				t.Fatal("committed reference lost its context")
			}
		})
	}
}

func TestContextGCRollbackAndCancellationIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	id := ReplayIdentity{1, time.Now().UTC(), 11}
	contextFragment(t, s, id)
	c := gcContext(t, 1)
	seed := contextTx(t, s)
	if err := PutReplayContext(ctx, seed, c); err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	writer := contextTx(t, s)
	if err := BindReplayContext(ctx, writer, id, "peers", c.Digest); err != nil {
		t.Fatal(err)
	}
	b, err := s.PruneReplayContexts(ctx, "", 10)
	if err != nil || b.Deleted != 0 {
		t.Fatal("FK writer lock ignored")
	}
	if err := writer.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `CREATE FUNCTION delay_context_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(2); RETURN OLD; END $$; CREATE TRIGGER delay_context_delete BEFORE DELETE ON replay_contexts FOR EACH ROW EXECUTE FUNCTION delay_context_delete()`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	b, err = s.PruneReplayContexts(short, "", 10)
	if err == nil || b.NextDigest != "" || b.Deleted != 0 {
		t.Fatal("interrupted batch advanced its committed cursor")
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM replay_contexts`).Scan(&n); err != nil || n != 1 {
		t.Fatal("cancelled GC lost a context")
	}
	if _, err := s.Pool.Exec(ctx, `DROP TRIGGER delay_context_delete ON replay_contexts`); err != nil {
		t.Fatal(err)
	}
	b, err = s.PruneReplayContexts(ctx, "", 10)
	if err != nil || b.Deleted != 1 {
		t.Fatalf("rolled-back binding pinned orphan: %+v %v", b, err)
	}
}
