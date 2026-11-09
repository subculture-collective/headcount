package score

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"testing"

	"git.subcult.tv/subculture-collective/headcount/internal/detect"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

func TestEncodeReplayUsesAcceptedContextVectors(t *testing.T) {
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
				Kind  string          `json:"kind"`
				Value json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal([]byte(v.Canonical), &envelope); err != nil {
				t.Fatal(err)
			}
			in := Snapshot{Schema: 1, Partners: []detect.Partner{{Login: "fixture", Onsets: []int64{3, 1}}}}
			var count int
			switch envelope.Kind {
			case "peers":
				if err := json.Unmarshal(envelope.Value, &in.Peers); err != nil {
					t.Fatal(err)
				}
				count = len(in.Peers)
			case "behavior_peers":
				if err := json.Unmarshal(envelope.Value, &in.BehaviorPeers); err != nil {
					t.Fatal(err)
				}
				count = len(in.BehaviorPeers)
			default:
				t.Fatal("unsupported fixture")
			}
			full, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			write, err := EncodeReplay(in)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(full)
			if write.InputDigest != hex.EncodeToString(hash[:]) {
				t.Fatal("digest no longer covers complete typed input")
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(write.Input, &fields); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(fields["partners"], []byte(`[3,1]`)) {
				t.Fatal("snapshot-specific partner evidence changed")
			}
			if count == 0 {
				if len(write.Contexts) != 0 || !bytes.Equal(fields[envelope.Kind], envelope.Value) {
					t.Fatal("empty/null array was factored or changed")
				}
				return
			}
			if len(write.Contexts) != 1 || write.Contexts[0].Digest != v.Digest || string(write.Contexts[0].Canonical) != v.Canonical {
				t.Fatal("production writer differs from accepted canonical bytes/hash")
			}
			if _, present := fields[envelope.Kind]; present {
				t.Fatal("referenced field remains inline")
			}
			fields[envelope.Kind] = envelope.Value
			assembled, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var restored Snapshot
			if err := json.Unmarshal(assembled, &restored); err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(restored)
			if err != nil || !bytes.Equal(full, after) {
				t.Fatal("factorization changed complete canonical input")
			}
		})
	}
}

func TestEncodeReplayRejectsUnsupportedOrNonfiniteInput(t *testing.T) {
	for _, in := range []Snapshot{{Schema: 2}, {Schema: 1, Peers: []store.PeerStat{{MedianViewers: math.NaN()}}}} {
		if _, err := EncodeReplay(in); err == nil {
			t.Fatal("invalid snapshot encoded")
		}
	}
	if New(nil, nil).ReferencedReplays {
		t.Fatal("referenced writer unexpectedly enabled by default")
	}
}
