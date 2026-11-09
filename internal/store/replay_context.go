package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReplayContextHashDomain separates frozen-context identities from packet digests.
const ReplayContextHashDomain = "headcount.replay-context.v1\x00"

// ReplayContext contains the producer's canonical envelope bytes. The storage
// boundary verifies identity and envelope metadata; typed encoding belongs to
// the producer and hydration reader, not a mutable cache or a JSONB round trip.
type ReplayContext struct {
	Digest    string
	Kind      string
	Schema    int
	Canonical []byte
}

func (c ReplayContext) validate() error {
	if c.Schema != 1 || (c.Kind != "peers" && c.Kind != "behavior_peers") {
		return fmt.Errorf("unsupported replay context kind or schema")
	}
	var envelope struct {
		Schema int               `json:"context_schema"`
		Kind   string            `json:"kind"`
		Value  []json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(c.Canonical, &envelope); err != nil {
		return fmt.Errorf("invalid replay context envelope")
	}
	if envelope.Schema != c.Schema || envelope.Kind != c.Kind || len(envelope.Value) == 0 {
		return fmt.Errorf("replay context envelope metadata mismatch or empty value")
	}
	h := sha256.New()
	h.Write([]byte(ReplayContextHashDomain))
	h.Write(c.Canonical)
	if hex.EncodeToString(h.Sum(nil)) != c.Digest {
		return fmt.Errorf("replay context digest mismatch")
	}
	return nil
}

// PutReplayContext inserts immutable content in the caller's score transaction.
// Equal content reuses identity; collisions/corruption fail. A key-share lock
// protects the object through reference creation and transaction completion.
// The caller must roll back on any error; this function never commits.
func PutReplayContext(ctx context.Context, tx pgx.Tx, c ReplayContext) error {
	_, _, err := putReplayContext(ctx, tx, c)
	return err
}

func putReplayContext(ctx context.Context, tx pgx.Tx, c ReplayContext) (bool, int64, error) {
	if err := c.validate(); err != nil {
		return false, 0, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO replay_contexts(digest,kind,context_schema,canonical) VALUES($1,$2,$3,$4) ON CONFLICT(digest) DO NOTHING`, c.Digest, c.Kind, c.Schema, c.Canonical)
	if err != nil {
		return false, 0, err
	}
	var stored ReplayContext
	var size int64
	err = tx.QueryRow(ctx, `SELECT digest,kind,context_schema,canonical,pg_column_size(canonical)::bigint FROM replay_contexts WHERE digest=$1 FOR KEY SHARE`, c.Digest).Scan(&stored.Digest, &stored.Kind, &stored.Schema, &stored.Canonical, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, fmt.Errorf("%w: %w", errReplayContextRace, err)
	}
	if err != nil {
		return false, 0, err
	}
	if stored.Kind != c.Kind || stored.Schema != c.Schema || !bytes.Equal(stored.Canonical, c.Canonical) {
		return false, 0, fmt.Errorf("replay context collision or corrupt stored content")
	}
	return tag.RowsAffected() == 1, size, nil
}

// ReplayIdentity identifies one immutable scoring observation and model version.
type ReplayIdentity struct {
	StreamID   int64
	ObservedAt time.Time
	Version    int
}

// BindReplayContext binds an omitted field of a version-2 fragment in the same
// transaction as its original input digest and score. Rebinding is prohibited.
// Foreign keys enforce field/kind agreement and protect referenced content.
func BindReplayContext(ctx context.Context, tx pgx.Tx, id ReplayIdentity, field, digest string) error {
	if field != "peers" && field != "behavior_peers" {
		return fmt.Errorf("unsupported replay context field")
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT storage_version=2 AND input_digest IS NOT NULL AND NOT(input ? $4)
 FROM score_snapshots WHERE stream_id=$1 AND observed_at=$2 AND version=$3 FOR UPDATE`, id.StreamID, id.ObservedAt, id.Version, field).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("context reference requires an omitted field in referenced storage")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO score_snapshot_contexts(stream_id,observed_at,version,field,context_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, id.StreamID, id.ObservedAt, id.Version, field, digest); err != nil {
		return err
	}
	var stored string
	if err := tx.QueryRow(ctx, `SELECT context_digest FROM score_snapshot_contexts WHERE stream_id=$1 AND observed_at=$2 AND version=$3 AND field=$4`, id.StreamID, id.ObservedAt, id.Version, field).Scan(&stored); err != nil {
		return err
	}
	if stored != digest {
		return fmt.Errorf("snapshot context reference collision")
	}
	return nil
}

// ReplayContextBinding carries raw verified frozen content to the hydration layer.
type ReplayContextBinding struct {
	Field   string
	Context ReplayContext
}

// ReplayContexts loads bindings through the current snapshot read transaction.
// It returns an explicit error for corrupt or unsupported content, never a cache.
func (s *Store) ReplayContexts(ctx context.Context, id ReplayIdentity) ([]ReplayContextBinding, error) {
	rows, err := s.reader().Query(ctx, `SELECT r.field,c.digest,c.kind,c.context_schema,c.canonical
 FROM score_snapshot_contexts r LEFT JOIN replay_contexts c ON c.digest=r.context_digest
 WHERE r.stream_id=$1 AND r.observed_at=$2 AND r.version=$3 ORDER BY r.field`, id.StreamID, id.ObservedAt, id.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplayContextBinding
	for rows.Next() {
		var b ReplayContextBinding
		if err := rows.Scan(&b.Field, &b.Context.Digest, &b.Context.Kind, &b.Context.Schema, &b.Context.Canonical); err != nil {
			return nil, err
		}
		if b.Field != b.Context.Kind {
			return nil, fmt.Errorf("replay context field mismatch")
		}
		if err := b.Context.validate(); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
