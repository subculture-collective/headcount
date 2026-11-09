package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/research"
	"git.subcult.tv/subculture-collective/headcount/internal/score"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

func TestHydratedExportCLIIntegration(t *testing.T) {
	s := researchTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	in := score.Snapshot{Schema: 1, ObservedAt: at.Add(3 * time.Hour), Ref: store.StreamRef{ID: 10, ChannelID: 1, StartedAt: at, FirstSeenAt: at, LastSeenAt: at.Add(2 * time.Hour)}, Peers: []store.PeerStat{{StreamID: 20, ChannelID: 2, MedianViewers: 1000, ChattersPerK: 2}}, BehaviorPeers: []store.BehaviorPeer{{StreamID: 20, ChannelID: 2, Metrics: map[string]float64{"b": 2, "a": 1}}}}
	for i := range 121 {
		in.Series.Viewers = append(in.Series.Viewers, store.ViewerPoint{TS: at.Add(time.Duration(i) * time.Minute), Viewers: 1000})
	}
	input, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	output := score.Evaluate(in, nil)
	out, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots(stream_id,observed_at,version,input,output) VALUES($1,$2,$3,$4,$5)`, in.Ref.ID, in.ObservedAt, output.Version, input, out); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool")
	if err := os.Mkdir(pool, 0700); err != nil {
		t.Fatal(err)
	}
	inline := filepath.Join(dir, "inline.json")
	export := func(path string) {
		t.Helper()
		if err := run([]string{"export", "-stream", "10", "-at", in.ObservedAt.Format(time.RFC3339Nano), "-out", path}); err != nil {
			t.Fatal(err)
		}
	}
	export(inline)
	want, err := research.ReadPacket(inline)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // Cleanup after commit/error.
	contexts := []store.ReplayContext{}
	for _, kind := range []string{"peers", "behavior_peers"} {
		canonical := append([]byte(`{"context_schema":1,"kind":"`+kind+`","value":`), fields[kind]...)
		canonical = append(canonical, '}')
		h := sha256.Sum256(append([]byte("headcount.replay-context.v1\x00"), canonical...))
		contexts = append(contexts, store.ReplayContext{Digest: hex.EncodeToString(h[:]), Kind: kind, Schema: 1, Canonical: canonical})
		delete(fields, kind)
	}
	fragment, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE score_snapshots SET storage_version=2,input=$1,input_digest=$2 WHERE stream_id=10`, fragment, want.Digest); err != nil {
		t.Fatal(err)
	}
	id := store.ReplayIdentity{StreamID: 10, ObservedAt: in.ObservedAt, Version: output.Version}
	for _, c := range contexts {
		if err := store.PutReplayContext(ctx, tx, c); err != nil {
			t.Fatal(err)
		}
		if err := store.BindReplayContext(ctx, tx, id, c.Kind, c.Digest); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	referenced := filepath.Join(pool, "referenced.json")
	export(referenced)
	got, err := research.ReadPacket(referenced)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("actual CLI changed inline packet after factoring")
	}
	info, err := os.Stat(referenced)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("export lost private file permissions")
	}
	// Mixed-format observations, including a previous scoring version.
	legacy := in
	legacy.Ref.ID = 11
	legacy.ObservedAt = legacy.ObservedAt.Add(time.Minute)
	legacyInput, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyOutput := output
	legacyOutput.Version = 9
	legacyOut, err := json.Marshal(legacyOutput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots(stream_id,observed_at,version,input,output) VALUES($1,$2,9,$3,$4)`, legacy.Ref.ID, legacy.ObservedAt, legacyInput, legacyOut); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"export", "-stream", strconv.FormatInt(legacy.Ref.ID, 10), "-out", filepath.Join(pool, "legacy.json")}); err != nil {
		t.Fatal(err)
	}
	roster := filepath.Join(dir, "controls.json")
	if err := research.WriteNew(roster, research.ControlRoster{Schema: 1, Source: "synthetic fixture", Candidates: []research.ControlCandidate{{Login: "fixture", ChannelID: 1, Status: "nominated"}}}); err != nil {
		t.Fatal(err)
	}
	cases := filepath.Join(dir, "cases.json")
	for _, args := range [][]string{
		{"replay", "-in", referenced, "-out", filepath.Join(dir, "replay.json")},
		{"review", "-in", referenced, "-out", filepath.Join(dir, "review.json")},
		{"case", "-in", referenced, "-out", cases},
		{"sample", "-pool", pool, "-n", "2", "-out", filepath.Join(dir, "sample.json")},
		{"control-cases", "-pool", pool, "-controls", roster, "-out", filepath.Join(dir, "controls-cases.json")},
		{"persistence", "-pool", pool, "-cases", cases, "-out", filepath.Join(dir, "persistence.json")},
	} {
		if err := run(args); err != nil {
			t.Fatalf("%s: %v", args[0], err)
		}
	}
	// Export is portable even after its database rows and contexts disappear.
	pruned, err := s.PruneReplayBatch(ctx, store.ReplayRetentionPolicy{AsOf: in.ObservedAt.Add(8 * 24 * time.Hour), Detail: 48 * time.Hour, Retention: 7 * 24 * time.Hour}, nil, 100)
	if err != nil || pruned.Deleted != 2 {
		t.Fatalf("normal snapshot expiry: %+v %v", pruned, err)
	}
	collected, err := s.PruneReplayContexts(ctx, "", 100)
	if err != nil || collected.Deleted != 2 {
		t.Fatalf("normal context expiry: %+v %v", collected, err)
	}
	if err := run([]string{"replay", "-in", referenced, "-out", filepath.Join(dir, "after-expiry.json")}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Result score.Result `json:"result"`
	}
	if err := readJSON(filepath.Join(dir, "after-expiry.json"), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Result, score.Evaluate(in, nil)) {
		t.Fatal("export replay changed after database expiry")
	}
}
