package score

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

// EncodeReplay factors nonempty frozen comparison arrays while preserving the
// complete typed-input digest. Empty/null arrays and snapshot-specific evidence
// remain inline; the returned fragment must never be evaluated directly.
func EncodeReplay(in Snapshot) (store.ReplayWrite, error) {
	if in.Schema != 1 {
		return store.ReplayWrite{}, fmt.Errorf("unsupported snapshot schema")
	}
	full, err := json.Marshal(in)
	if err != nil {
		return store.ReplayWrite{}, err
	}
	h := sha256.Sum256(full)
	out := store.ReplayWrite{InputDigest: hex.EncodeToString(h[:])}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(full, &fields); err != nil {
		return store.ReplayWrite{}, err
	}
	for _, field := range []struct {
		kind  string
		value any
		count int
	}{{"peers", in.Peers, len(in.Peers)}, {"behavior_peers", in.BehaviorPeers, len(in.BehaviorPeers)}} {
		if field.count == 0 {
			continue
		}
		canonical, err := json.Marshal(struct {
			Schema int    `json:"context_schema"`
			Kind   string `json:"kind"`
			Value  any    `json:"value"`
		}{1, field.kind, field.value})
		if err != nil {
			return store.ReplayWrite{}, err
		}
		h := sha256.Sum256(append([]byte(store.ReplayContextHashDomain), canonical...))
		out.Contexts = append(out.Contexts, store.ReplayContext{Digest: hex.EncodeToString(h[:]), Kind: field.kind, Schema: 1, Canonical: canonical})
		delete(fields, field.kind)
	}
	out.Input, err = json.Marshal(fields)
	if err != nil {
		return store.ReplayWrite{}, err
	}
	return out, nil
}
