package store

import (
	"context"
	"time"
)

// StoredReplay is private storage evidence, not an evaluable input or packet.
// References and fragment bytes must pass typed hydration before export.
type StoredReplay struct {
	Identity       ReplayIdentity
	StorageVersion int
	InputDigest    *string
	Input, Output  []byte
	Contexts       []ReplayContextBinding
}

// ReplaySnapshot reads the selected observation and frozen contexts through one
// repeatable-read transaction. Readers already in a snapshot reuse that view.
func (s *Store) ReplaySnapshot(ctx context.Context, streamID int64, at *time.Time) (StoredReplay, error) {
	var out StoredReplay
	load := func(view *Store, _ time.Time) error {
		err := view.reader().QueryRow(ctx, `SELECT stream_id,observed_at,version,storage_version,input_digest,input,output
 FROM score_snapshots WHERE stream_id=$1 AND ($2::timestamptz IS NULL OR observed_at=$2)
 ORDER BY observed_at DESC,version DESC LIMIT 1`, streamID, at).Scan(&out.Identity.StreamID, &out.Identity.ObservedAt, &out.Identity.Version, &out.StorageVersion, &out.InputDigest, &out.Input, &out.Output)
		if err != nil {
			return err
		}
		out.Contexts, err = view.ReplayContexts(ctx, out.Identity)
		return err
	}
	var err error
	if s.read != nil {
		err = load(s, time.Time{})
	} else {
		err = s.ReadSnapshot(ctx, load)
	}
	if err != nil {
		return StoredReplay{}, err
	}
	return out, nil
}
