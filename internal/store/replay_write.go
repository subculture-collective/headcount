package store

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ReplayWrite is producer-encoded private storage, with a digest of the complete
// typed input. Context canonical bytes and array ordering are immutable.
type ReplayWrite struct {
	Input       []byte
	InputDigest string
	Contexts    []ReplayContext
}

// ReplayWriteStats reports acknowledged inserts and compression-aware referenced
// datum sizes. It excludes relation/index overhead and is zero on failure.
type ReplayWriteStats struct {
	StorageVersion       int
	SnapshotInserted     bool
	NewContexts          int
	ReusedContexts       int
	SnapshotPayloadBytes int64
	NewContextBytes      int64
}

var errReplayContextRace = errors.New("replay context expired before binding")

// SaveReferencedScore commits the score, caches, fragment and context bindings
// together. A GC race retries the whole transaction; evidence collisions do not.
// Existing snapshot identities retain their original inline/referenced format.
func (s *Store) SaveReferencedScore(ctx context.Context, sc Score, replay ReplayWrite) (ReplayWriteStats, error) {
	if sc.SnapshotInput == nil || sc.SnapshotOutput == nil {
		return ReplayWriteStats{}, fmt.Errorf("referenced score requires complete replay input and output")
	}
	var err error
	for range 3 {
		var stats ReplayWriteStats
		stats, err = s.saveScore(ctx, sc, &replay)
		if err == nil {
			return stats, nil
		}
		if !retryReplayWrite(err) || ctx.Err() != nil {
			break
		}
	}
	return ReplayWriteStats{}, err
}

func retryReplayWrite(err error) bool {
	if errors.Is(err, errReplayContextRace) {
		return true
	}
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503" && pg.ConstraintName == "score_snapshot_contexts_context_digest_field_fkey"
}

func canonicalContext(kind string, value json.RawMessage) ([]byte, error) {
	return json.Marshal(struct {
		Schema int             `json:"context_schema"`
		Kind   string          `json:"kind"`
		Value  json.RawMessage `json:"value"`
	}{1, kind, value})
}

func validateReplayWrite(full []byte, replay ReplayWrite) error {
	h := sha256.Sum256(full)
	if replay.InputDigest != hex.EncodeToString(h[:]) {
		return fmt.Errorf("replay full-input digest mismatch")
	}
	var fields, fragment map[string]json.RawMessage
	if err := json.Unmarshal(full, &fields); err != nil || fields == nil {
		return fmt.Errorf("invalid complete replay input")
	}
	if err := json.Unmarshal(replay.Input, &fragment); err != nil || fragment == nil {
		return fmt.Errorf("invalid replay fragment")
	}
	seen := map[string]bool{}
	for _, c := range replay.Contexts {
		if err := c.validate(); err != nil {
			return err
		}
		if seen[c.Kind] {
			return fmt.Errorf("duplicate replay context field")
		}
		seen[c.Kind] = true
		value, ok := fields[c.Kind]
		if !ok {
			return fmt.Errorf("replay context absent from complete input")
		}
		canonical, err := canonicalContext(c.Kind, value)
		if err != nil {
			return err
		}
		if !bytes.Equal(canonical, c.Canonical) {
			return fmt.Errorf("replay context differs from complete input")
		}
		if _, present := fragment[c.Kind]; present {
			return fmt.Errorf("replay field is both inline and referenced")
		}
		delete(fields, c.Kind)
	}
	want, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	got, err := json.Marshal(fragment)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("replay fragment differs from complete input")
	}
	return nil
}

func writeReplay(ctx context.Context, tx pgx.Tx, sc Score, replay *ReplayWrite) (ReplayWriteStats, error) {
	full, err := json.Marshal(sc.SnapshotInput)
	if err != nil {
		return ReplayWriteStats{}, err
	}
	output, err := json.Marshal(sc.SnapshotOutput)
	if err != nil {
		return ReplayWriteStats{}, err
	}
	input, version := full, 1
	var digest *string
	var contexts []ReplayContext
	if replay != nil {
		if err := validateReplayWrite(full, *replay); err != nil {
			return ReplayWriteStats{}, err
		}
		if len(replay.Contexts) > 0 {
			input, version, digest = replay.Input, 2, &replay.InputDigest
			contexts = slices.Clone(replay.Contexts)
			// Consistent lock order prevents crossing shared-context writers.
			slices.SortFunc(contexts, func(a, b ReplayContext) int { return cmp.Compare(a.Digest, b.Digest) })
		}
	}
	id := ReplayIdentity{sc.StreamID, sc.ObservedAt, sc.Version}
	tag, err := tx.Exec(ctx, `INSERT INTO score_snapshots(stream_id,observed_at,version,input,output,storage_version,input_digest)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, id.StreamID, id.ObservedAt, id.Version, input, output, version, digest)
	if err != nil {
		return ReplayWriteStats{}, err
	}
	stats := ReplayWriteStats{StorageVersion: version, SnapshotInserted: tag.RowsAffected() == 1}
	if !stats.SnapshotInserted {
		storedVersion, err := compareReplay(ctx, tx, id, full, output)
		stats.StorageVersion = storedVersion
		return stats, err
	}
	for _, c := range contexts {
		created, size, err := putReplayContext(ctx, tx, c)
		if err != nil {
			return ReplayWriteStats{}, err
		}
		if err := BindReplayContext(ctx, tx, id, c.Kind, c.Digest); err != nil {
			return ReplayWriteStats{}, err
		}
		if created {
			stats.NewContexts++
			stats.NewContextBytes += size
		} else {
			stats.ReusedContexts++
		}
	}
	if replay != nil {
		err = tx.QueryRow(ctx, `SELECT pg_column_size(input)::bigint+pg_column_size(output)::bigint
 FROM score_snapshots WHERE stream_id=$1 AND observed_at=$2 AND version=$3`, id.StreamID, id.ObservedAt, id.Version).Scan(&stats.SnapshotPayloadBytes)
	}
	return stats, err
}

// compareReplay checks complete evidence on a retry, independently of its stored
// format. Contexts must match the producer's complete canonical field bytes.
func compareReplay(ctx context.Context, tx pgx.Tx, id ReplayIdentity, full, output []byte) (int, error) {
	var stored StoredReplay
	err := tx.QueryRow(ctx, `SELECT storage_version,input_digest,input,output FROM score_snapshots
 WHERE stream_id=$1 AND observed_at=$2 AND version=$3 FOR UPDATE`, id.StreamID, id.ObservedAt, id.Version).Scan(&stored.StorageVersion, &stored.InputDigest, &stored.Input, &stored.Output)
	if err != nil {
		return 0, err
	}
	stored.Contexts, err = (&Store{read: tx}).ReplayContexts(ctx, id)
	if err != nil {
		return 0, err
	}
	if stored.InputDigest != nil {
		h := sha256.Sum256(full)
		if *stored.InputDigest != hex.EncodeToString(h[:]) {
			return 0, fmt.Errorf("snapshot identity collision or corrupt full-input digest")
		}
	}
	switch stored.StorageVersion {
	case 1:
		if len(stored.Contexts) != 0 {
			return 0, fmt.Errorf("inline replay has context references")
		}
	case 2:
		if stored.InputDigest == nil || len(stored.Contexts) == 0 {
			return 0, fmt.Errorf("referenced replay lacks digest or contexts")
		}
		var fragment, expected map[string]json.RawMessage
		if err := json.Unmarshal(stored.Input, &fragment); err != nil || fragment == nil {
			return 0, fmt.Errorf("invalid stored replay fragment")
		}
		if err := json.Unmarshal(full, &expected); err != nil || expected == nil {
			return 0, fmt.Errorf("invalid complete replay input")
		}
		for _, binding := range stored.Contexts {
			if _, present := fragment[binding.Field]; present {
				return 0, fmt.Errorf("replay field is both inline and referenced")
			}
			value, present := expected[binding.Field]
			if !present {
				return 0, fmt.Errorf("snapshot identity collision with different context")
			}
			canonical, err := canonicalContext(binding.Field, value)
			if err != nil {
				return 0, err
			}
			if !bytes.Equal(canonical, binding.Context.Canonical) {
				return 0, fmt.Errorf("snapshot identity collision or noncanonical stored context")
			}
			fragment[binding.Field] = value
		}
		stored.Input, err = json.Marshal(fragment)
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unsupported replay storage version")
	}
	var same bool
	if err := tx.QueryRow(ctx, `SELECT $1::jsonb=$2::jsonb AND $3::jsonb=$4::jsonb`, stored.Input, full, stored.Output, output).Scan(&same); err != nil {
		return 0, err
	}
	if !same {
		return 0, fmt.Errorf("snapshot identity collision with different evidence")
	}
	return stored.StorageVersion, nil
}
