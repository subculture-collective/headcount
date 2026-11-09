package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestReplaySnapshotConsistentDuringExpiryIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id := ReplayIdentity{1, time.Now().UTC(), 11}
	contextFragment(t, s, id)
	c := fixtureContext(t)
	tx := contextTx(t, s)
	if err := PutReplayContext(ctx, tx, c); err != nil {
		t.Fatal(err)
	}
	if err := BindReplayContext(ctx, tx, id, "peers", c.Digest); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadSnapshot(ctx, func(view *Store, _ time.Time) error {
		first, err := view.ReplaySnapshot(ctx, id.StreamID, nil)
		if err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, `DELETE FROM score_snapshots; DELETE FROM replay_contexts`); err != nil {
			return err
		}
		second, err := view.ReplaySnapshot(ctx, id.StreamID, &id.ObservedAt)
		if err != nil {
			return err
		}
		if first.Identity != second.Identity || len(second.Contexts) != 1 || !bytes.Equal(second.Contexts[0].Context.Canonical, c.Canonical) {
			t.Fatal("expiry changed a consistent snapshot view")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaySnapshot(ctx, id.StreamID, nil); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired snapshot: %v", err)
	}
}

func TestReplaySnapshotMissingOrCorruptContextIntegration(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "missing"}[missing], func(t *testing.T) {
			s := bugTestStore(t)
			ctx := context.Background()
			id := ReplayIdentity{1, time.Now().UTC(), 11}
			contextFragment(t, s, id)
			c := fixtureContext(t)
			tx := contextTx(t, s)
			if err := PutReplayContext(ctx, tx, c); err != nil {
				t.Fatal(err)
			}
			if err := BindReplayContext(ctx, tx, id, "peers", c.Digest); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE replay_contexts SET canonical='corrupt'`
			if missing {
				query = `BEGIN; SET LOCAL session_replication_role='replica'; DELETE FROM replay_contexts; COMMIT;`
			}
			if _, err := s.Pool.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
			raw, err := s.ReplaySnapshot(ctx, id.StreamID, nil)
			if err == nil || raw.Input != nil {
				t.Fatal("missing/corrupt context became partial evidence")
			}
		})
	}
}
