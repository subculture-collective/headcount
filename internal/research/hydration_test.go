package research

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/score"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

func hydrationFixture(t *testing.T, referenced bool, version int) (store.StoredReplay, Packet) {
	t.Helper()
	in := fixture(1, "development", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), false).Input
	in.Peers = []store.PeerStat{{StreamID: 20, ChannelID: 2, MedianViewers: 1000, ChattersPerK: 2}, {MetricVersion: 2, StreamID: 30, ChannelID: 3, MedianViewers: 2000, ChattersPerK: 1}}
	in.BehaviorPeers = []store.BehaviorPeer{{StreamID: 20, ChannelID: 2, Metrics: map[string]float64{"b": 2, "a": 1}}}
	out := score.Evaluate(in, nil)
	out.Version = version
	digest, err := Digest(in)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	raw := store.StoredReplay{Identity: store.ReplayIdentity{StreamID: in.Ref.ID, ObservedAt: in.ObservedAt, Version: version}, StorageVersion: 1, Input: input, Output: output}
	if referenced {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(input, &fields); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"peers", "behavior_peers"} {
			canonical := append([]byte(`{"context_schema":1,"kind":"`+kind+`","value":`), fields[kind]...)
			canonical = append(canonical, '}')
			hash := sha256.Sum256(append([]byte("headcount.replay-context.v1\x00"), canonical...))
			raw.Contexts = append(raw.Contexts, store.ReplayContextBinding{Field: kind, Context: store.ReplayContext{Digest: hex.EncodeToString(hash[:]), Kind: kind, Schema: 1, Canonical: canonical}})
			delete(fields, kind)
		}
		raw.Input, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		raw.StorageVersion = 2
		raw.InputDigest = &digest
	}
	return raw, Packet{Input: in, Output: out, Digest: digest}
}

func TestHydratedPacketsPreserveLegacyChecksumsAndOutput(t *testing.T) {
	for _, version := range []int{9, 11} {
		for _, referenced := range []bool{false, true} {
			raw, want := hydrationFixture(t, referenced, version)
			got, err := HydrateReplay(raw)
			if err != nil {
				t.Fatal(err)
			}
			a, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatal("hydration changed packet digest, field order, legacy metadata or output")
			}
			if _, err := Sample([]Packet{got}, 1, 17); err != nil {
				t.Fatal(err)
			}
			if _, err := ControlCases([]Packet{got}, ControlRoster{Schema: 1, Source: "synthetic nomination", Candidates: []ControlCandidate{{Login: "fixture", ChannelID: 1, Status: "nominated"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := Persistence([]Packet{got}, nil, "audience", .3, .1); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHydrationRejectsIncompleteOrCorruptEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*store.StoredReplay)
	}{
		{"unknown storage", func(r *store.StoredReplay) { r.StorageVersion = 3 }},
		{"missing digest", func(r *store.StoredReplay) { r.InputDigest = nil }},
		{"missing context", func(r *store.StoredReplay) { r.Contexts = r.Contexts[:1] }},
		{"no contexts", func(r *store.StoredReplay) { r.Contexts = nil }},
		{"digest mismatch", func(r *store.StoredReplay) { s := "bad"; r.InputDigest = &s }},
		{"duplicate field", func(r *store.StoredReplay) { r.Contexts = append(r.Contexts, r.Contexts[0]) }},
		{"inline and referenced", func(r *store.StoredReplay) {
			r.Input = bytes.Replace(r.Input, []byte(`"schema":1`), []byte(`"schema":1,"peers":null`), 1)
		}},
		{"kind mismatch", func(r *store.StoredReplay) { r.Contexts[0].Context.Kind = "behavior_peers" }},
		{"unsupported context schema", func(r *store.StoredReplay) { r.Contexts[0].Context.Schema = 2 }},
		{"context digest", func(r *store.StoredReplay) { r.Contexts[0].Context.Digest = "bad" }},
		{"noncanonical context", func(r *store.StoredReplay) {
			r.Contexts[0].Context.Canonical = append([]byte(" "), r.Contexts[0].Context.Canonical...)
		}},
		{"wrong observation identity", func(r *store.StoredReplay) { r.Identity.ObservedAt = r.Identity.ObservedAt.Add(time.Second) }},
		{"wrong model identity", func(r *store.StoredReplay) { r.Identity.Version = 10 }},
		{"invalid fragment", func(r *store.StoredReplay) { r.Input = []byte("null") }},
		{"inline references", func(r *store.StoredReplay) { r.StorageVersion = 1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := hydrationFixture(t, true, 11)
			tc.mutate(&raw)
			got, err := HydrateReplay(raw)
			if err == nil || got.Input.Schema != 0 {
				t.Fatalf("corrupt input produced usable packet: %v", err)
			}
		})
	}
}

func TestHydrationKeepsInlineNilAndEmptyPeers(t *testing.T) {
	for _, empty := range []bool{false, true} {
		raw, want := hydrationFixture(t, true, 11)
		if empty {
			want.Input.Peers = []store.PeerStat{}
		} else {
			want.Input.Peers = nil
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw.Input, &fields); err != nil {
			t.Fatal(err)
		}
		peers, err := json.Marshal(want.Input.Peers)
		if err != nil {
			t.Fatal(err)
		}
		fields["peers"] = peers
		raw.Input, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		raw.Contexts = raw.Contexts[1:]
		digest, err := Digest(want.Input)
		if err != nil {
			t.Fatal(err)
		}
		raw.InputDigest = &digest
		got, err := HydrateReplay(raw)
		if err != nil {
			t.Fatal(err)
		}
		if (got.Input.Peers == nil) != (!empty) || got.Digest != digest {
			t.Fatal("nil and empty peers lost their canonical distinction")
		}
	}
}
