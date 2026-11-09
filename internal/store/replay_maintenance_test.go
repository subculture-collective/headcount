package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReplayMaintenanceIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `ALTER TABLE score_snapshots SET(autovacuum_vacuum_scale_factor=0.02,autovacuum_vacuum_threshold=100,toast.autovacuum_vacuum_scale_factor=0.02,toast.autovacuum_vacuum_threshold=100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `VACUUM (ANALYZE) public.score_snapshots`); err != nil {
		t.Fatal(err)
	}
	r, err := s.PreviewReplayRetention(ctx, ReplayRetentionPolicy{AsOf: time.Now(), Retention: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	m := r.Storage.Maintenance
	if m.Settings["autovacuum"] != "on" || m.Settings["track_counts"] != "on" || len(m.Relations) != 2 {
		t.Fatalf("missing maintenance settings: %+v", m)
	}
	var allocated int64
	for _, rel := range m.Relations {
		allocated += rel.TableBytes + rel.IndexBytes
		if !strings.Contains(strings.Join(rel.Options, ","), "autovacuum_vacuum_scale_factor=0.02") {
			t.Fatalf("override absent: %+v", rel)
		}
		if rel.TableBytes < 0 || rel.IndexBytes < 0 || rel.LiveRowsEstimate < 0 || rel.DeadRowsEstimate < 0 {
			t.Fatal("invalid relation accounting")
		}
		if rel.Relation == "score_snapshots" && (rel.LastVacuum == nil || rel.LastAnalyze == nil || rel.VacuumCount < 1) {
			t.Fatalf("manual vacuum not visible: %+v", rel)
		}
	}
	if allocated != r.Storage.TotalAllocatedBytes {
		t.Fatal("maintenance allocation overlaps heap and TOAST")
	}
}
