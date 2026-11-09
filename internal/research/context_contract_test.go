package research

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/detect"
	"git.subcult.tv/subculture-collective/headcount/internal/score"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

// These vectors specify a future storage contract; no production writer uses it.
func TestReplayContextContractVectors(t *testing.T) {
	raw, err := os.ReadFile("../../docs/fixtures/replay-context-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name      string `json:"name"`
		Canonical string `json:"canonical_utf8"`
		Digest    string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			var envelope struct {
				Schema int             `json:"context_schema"`
				Kind   string          `json:"kind"`
				Value  json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal([]byte(v.Canonical), &envelope); err != nil {
				t.Fatal(err)
			}
			var value any
			switch envelope.Kind {
			case "peers":
				value = new([]store.PeerStat)
			case "partners":
				value = new([]detect.Partner)
			case "behavior_peers":
				value = new([]store.BehaviorPeer)
			default:
				t.Fatalf("unknown fixture kind %q", envelope.Kind)
			}
			if err := json.Unmarshal(envelope.Value, value); err != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(struct {
				Schema int    `json:"context_schema"`
				Kind   string `json:"kind"`
				Value  any    `json:"value"`
			}{envelope.Schema, envelope.Kind, value})
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Schema != 1 || string(canonical) != v.Canonical {
				t.Fatal("typed canonical bytes differ from the contract")
			}
			h := sha256.Sum256(append([]byte("headcount.replay-context.v1\x00"), canonical...))
			if hex.EncodeToString(h[:]) != v.Digest {
				t.Fatal("context digest differs from the contract")
			}
		})
	}
}

func TestReplayContextFieldDecompositionPreservesPacket(t *testing.T) {
	in := fixture(1, "development", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), false).Input
	in.Peers = []store.PeerStat{{MetricVersion: 2, StreamID: 20, ChannelID: 2, MedianViewers: 1000, ChattersPerK: 2}}
	in.Partners = []detect.Partner{{Login: "fixture-two", OverlapMinutes: 60, Onsets: []int64{1, 3}}}
	in.BehaviorPeers = []store.BehaviorPeer{{StreamID: 20, ChannelID: 2, Metrics: map[string]float64{"b": 2, "a": 1}}}
	before, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var fragment map[string]json.RawMessage
	if err := json.Unmarshal(before, &fragment); err != nil {
		t.Fatal(err)
	}
	contexts := map[string]json.RawMessage{}
	for _, field := range []string{"peers", "partners", "behavior_peers"} {
		contexts[field] = fragment[field]
		delete(fragment, field)
	}
	for field, value := range contexts {
		fragment[field] = value
	}
	raw, err := json.Marshal(fragment)
	if err != nil {
		t.Fatal(err)
	}
	var hydrated score.Snapshot
	if err := json.Unmarshal(raw, &hydrated); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(hydrated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("field decomposition changed canonical packet input")
	}
	digest, err := Digest(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := (Packet{Input: hydrated, Digest: digest}).Verify(); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(score.Evaluate(in, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(score.Evaluate(hydrated, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("field decomposition changed replay output")
	}
}
