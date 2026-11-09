package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func fixtureContext(t *testing.T) ReplayContext {
	t.Helper()
	b, err := os.ReadFile("../../docs/fixtures/replay-context-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Canonical string `json:"canonical_utf8"`
		Digest    string `json:"sha256"`
	}
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	return ReplayContext{vectors[0].Digest, "peers", 1, []byte(vectors[0].Canonical)}
}

func contextTx(t *testing.T, s *Store) pgx.Tx {
	t.Helper()
	tx, err := s.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) }) // Best-effort after commit/rollback.
	return tx
}

func contextFragment(t *testing.T, s *Store, id ReplayIdentity) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO score_snapshots(stream_id,observed_at,version,input,output,storage_version,input_digest) VALUES($1,$2,$3,'{}','{}',2,repeat('a',64))`, id.StreamID, id.ObservedAt, id.Version); err != nil {
		t.Fatal(err)
	}
}

func TestReplayContextStorageIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	c := fixtureContext(t)
	id := ReplayIdentity{1, time.Now().UTC().Truncate(time.Microsecond), 11}
	contextFragment(t, s, id)
	tx := contextTx(t, s)
	for range 2 {
		if err := PutReplayContext(ctx, tx, c); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := BindReplayContext(ctx, tx, id, "peers", c.Digest); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM replay_contexts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("dedup count=%d err=%v", n, err)
	}
	if err := s.ReadSnapshot(ctx, func(view *Store, _ time.Time) error {
		rows, err := view.ReplayContexts(ctx, id)
		if err == nil && (len(rows) != 1 || rows[0].Field != "peers" || !bytes.Equal(rows[0].Context.Canonical, c.Canonical)) {
			t.Fatal("round trip differs")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM replay_contexts WHERE digest=$1`, c.Digest); err == nil {
		t.Fatal("referenced context deleted")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM score_snapshots WHERE stream_id=$1`, id.StreamID); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM score_snapshot_contexts`).Scan(&n); err != nil || n != 0 {
		t.Fatal("snapshot reference did not cascade")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM replay_contexts WHERE digest=$1`, c.Digest); err != nil {
		t.Fatal(err)
	}
	// A failed surrounding score transaction leaves no new context or reference.
	tx = contextTx(t, s)
	if err := PutReplayContext(ctx, tx, c); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM replay_contexts`).Scan(&n); err != nil || n != 0 {
		t.Fatal("context survived rollback")
	}
}

func TestReplayContextCorruptionAndPrivacyIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	c := fixtureContext(t)
	id := ReplayIdentity{1, time.Now().UTC(), 11}
	contextFragment(t, s, id)
	for _, mutate := range []func(*ReplayContext){func(c *ReplayContext) { c.Digest = "bad" }, func(c *ReplayContext) { c.Schema = 2 }, func(c *ReplayContext) { c.Kind = "partners" }, func(c *ReplayContext) { c.Canonical = []byte("null") }} {
		bad := c
		mutate(&bad)
		tx := contextTx(t, s)
		if err := PutReplayContext(ctx, tx, bad); err == nil {
			t.Fatal("invalid context accepted")
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tx := contextTx(t, s)
	if err := PutReplayContext(ctx, tx, c); err != nil {
		t.Fatal(err)
	}
	if err := BindReplayContext(ctx, tx, id, "behavior_peers", c.Digest); err == nil {
		t.Fatal("field/kind mismatch accepted")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a corrupted row/digest collision without needing a SHA-256 collision.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO replay_contexts VALUES($1,'peers',1,'corrupted',now())`, c.Digest); err != nil {
		t.Fatal(err)
	}
	tx = contextTx(t, s)
	if err := PutReplayContext(ctx, tx, c); err == nil {
		t.Fatal("existing different bytes overwritten")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Default grants must not expose new tables or any write capability.
	for _, table := range []string{"replay_contexts", "score_snapshot_contexts"} {
		var exposed bool
		if err := s.Pool.QueryRow(ctx, `SELECT has_table_privilege('susq_api',$1,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')`, table).Scan(&exposed); err != nil || exposed {
			t.Fatalf("private role permission on %s: %v %v", table, exposed, err)
		}
		tx = contextTx(t, s)
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE susq_api`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT * FROM `+pgx.Identifier{table}.Sanitize()); err == nil {
			t.Fatal("private SELECT allowed")
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
}

func TestReplayContextConcurrentOwnershipIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	c := fixtureContext(t)
	first := contextTx(t, s)
	if err := PutReplayContext(ctx, first, c); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := PutReplayContext(ctx, tx, c); err != nil {
			done <- err
			return
		}
		done <- tx.Commit(ctx)
	}()
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	id := ReplayIdentity{1, time.Now().UTC(), 11}
	contextFragment(t, s, id)
	writer := contextTx(t, s)
	if err := PutReplayContext(ctx, writer, c); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := s.Pool.Exec(short, `DELETE FROM replay_contexts WHERE digest=$1`, c.Digest); err == nil {
		t.Fatal("GC ignored writer key-share lock")
	}
	if err := BindReplayContext(ctx, writer, id, "peers", c.Digest); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Changed valid content cannot rebind an immutable field.
	other := c
	other.Canonical = bytes.ReplaceAll(c.Canonical, []byte("100"), []byte("101"))
	h := sha256.Sum256(append([]byte("headcount.replay-context.v1\x00"), other.Canonical...))
	other.Digest = hex.EncodeToString(h[:])
	if other.Digest == c.Digest {
		t.Fatal("fixture did not change")
	}
	tx := contextTx(t, s)
	if err := PutReplayContext(ctx, tx, other); err != nil {
		t.Fatal(err)
	}
	if err := BindReplayContext(ctx, tx, id, "peers", other.Digest); err == nil {
		t.Fatal("immutable binding replaced")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}
