package research

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

// HydrateReplay reconstructs and verifies a portable schema-1 packet. It never
// evaluates sparse input or falls back to current mutable comparison caches.
func HydrateReplay(raw store.StoredReplay) (Packet, error) {
	var p Packet
	if raw.StorageVersion != 1 && raw.StorageVersion != 2 {
		return Packet{}, fmt.Errorf("unsupported replay storage version")
	}
	if raw.StorageVersion == 1 && len(raw.Contexts) != 0 {
		return Packet{}, fmt.Errorf("inline replay has context references")
	}
	if raw.StorageVersion == 2 && (raw.InputDigest == nil || len(raw.Contexts) == 0) {
		return Packet{}, fmt.Errorf("referenced replay lacks full digest or contexts")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw.Input, &fields); err != nil || fields == nil {
		return Packet{}, fmt.Errorf("invalid replay input object")
	}
	seen := map[string]bool{}
	for _, binding := range raw.Contexts {
		if seen[binding.Field] {
			return Packet{}, fmt.Errorf("duplicate replay context binding")
		}
		seen[binding.Field] = true
		if _, present := fields[binding.Field]; present {
			return Packet{}, fmt.Errorf("replay field is both inline and referenced")
		}
		if binding.Field != binding.Context.Kind {
			return Packet{}, fmt.Errorf("replay context binding kind mismatch")
		}
		value, err := decodeReplayContext(binding.Context)
		if err != nil {
			return Packet{}, err
		}
		fields[binding.Field] = value
	}
	input, err := json.Marshal(fields)
	if err != nil {
		return Packet{}, err
	}
	if err := json.Unmarshal(input, &p.Input); err != nil {
		return Packet{}, fmt.Errorf("invalid hydrated replay input")
	}
	if err := json.Unmarshal(raw.Output, &p.Output); err != nil {
		return Packet{}, fmt.Errorf("invalid replay output")
	}
	p.Digest, err = Digest(p.Input)
	if err != nil {
		return Packet{}, err
	}
	if raw.InputDigest != nil && p.Digest != *raw.InputDigest {
		return Packet{}, fmt.Errorf("hydrated replay full-input checksum mismatch")
	}
	if err := p.Verify(); err != nil {
		return Packet{}, err
	}
	if p.Input.Ref.ID != raw.Identity.StreamID || !p.Input.ObservedAt.Equal(raw.Identity.ObservedAt) || p.Output.Version != raw.Identity.Version {
		return Packet{}, fmt.Errorf("replay storage identity mismatch")
	}
	return p, nil
}

func decodeReplayContext(c store.ReplayContext) (json.RawMessage, error) {
	var envelope struct {
		Schema int             `json:"context_schema"`
		Kind   string          `json:"kind"`
		Value  json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(c.Canonical, &envelope); err != nil {
		return nil, fmt.Errorf("invalid frozen context envelope")
	}
	if c.Schema != 1 || envelope.Schema != 1 || c.Kind != envelope.Kind {
		return nil, fmt.Errorf("unsupported or mismatched frozen context schema")
	}
	var value any
	switch c.Kind {
	case "peers":
		value = new([]store.PeerStat)
	case "behavior_peers":
		value = new([]store.BehaviorPeer)
	default:
		return nil, fmt.Errorf("unsupported frozen context kind")
	}
	if err := json.Unmarshal(envelope.Value, value); err != nil {
		return nil, fmt.Errorf("invalid frozen context value")
	}
	canonical, err := json.Marshal(struct {
		Schema int    `json:"context_schema"`
		Kind   string `json:"kind"`
		Value  any    `json:"value"`
	}{1, c.Kind, value})
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, c.Canonical) || bytes.Equal(envelope.Value, []byte("null")) || bytes.Equal(envelope.Value, []byte("[]")) {
		return nil, fmt.Errorf("frozen context is noncanonical or empty")
	}
	h := sha256.Sum256(append([]byte(store.ReplayContextHashDomain), canonical...))
	if hex.EncodeToString(h[:]) != c.Digest {
		return nil, fmt.Errorf("frozen context checksum mismatch")
	}
	return envelope.Value, nil
}
