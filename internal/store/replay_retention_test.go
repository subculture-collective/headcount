package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReplayRetentionPolicy(t *testing.T) {
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		policy ReplayRetentionPolicy
		valid  bool
	}{
		{"missing time", ReplayRetentionPolicy{}, false},
		{"negative detail", ReplayRetentionPolicy{at, -1, 7 * 24 * time.Hour}, false},
		{"negative outer", ReplayRetentionPolicy{at, 0, -1}, false},
		{"inverted", ReplayRetentionPolicy{at, 8 * 24 * time.Hour, 7 * 24 * time.Hour}, false},
		{"disabled", ReplayRetentionPolicy{at, 48 * time.Hour, 0}, true},
		{"legacy expiry", ReplayRetentionPolicy{at, 0, 7 * 24 * time.Hour}, true},
		{"equal windows", ReplayRetentionPolicy{at, 7 * 24 * time.Hour, 7 * 24 * time.Hour}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.policy.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestReplayRetentionPreviewIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	var beforeMigrations int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&beforeMigrations); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := ReplayRetentionPolicy{AsOf: at, Detail: 48 * time.Hour, Retention: 7 * 24 * time.Hour}
	fixtures := []struct {
		name, disposition string
		stream            int64
		version           int
		delta             time.Duration
	}{
		{"expired", "expired", 1, 11, -8 * 24 * time.Hour},
		{"outer boundary", "thinnable", 1, 11, -7 * 24 * time.Hour},
		{"intermediate", "thinnable", 1, 11, -6 * 24 * time.Hour},
		{"detail boundary", "retained", 1, 11, -48 * time.Hour},
		{"recent", "retained", 1, 11, -time.Hour},
		{"other version", "retained", 1, 9, -6 * 24 * time.Hour},
		{"latest before future", "retained", 2, 11, -6 * 24 * time.Hour},
		{"future", "retained", 2, 11, time.Hour},
		{"expired singleton", "expired", 3, 11, -8 * 24 * time.Hour},
		{"older final", "thinnable", 4, 11, -3 * 24 * time.Hour},
		{"new reopened", "retained", 4, 11, -49 * time.Hour},
		{"before detail boundary", "thinnable", 5, 11, -48*time.Hour - time.Microsecond},
		{"after detail boundary", "retained", 5, 11, -47 * time.Hour},
	}
	for _, f := range fixtures {
		_, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots(stream_id,observed_at,version,input,output)
 VALUES($1,$2,$3,jsonb_build_object('case',$4::text),'{}')`, f.stream, at.Add(f.delta), f.version, f.name)
		if err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := func() string {
		t.Helper()
		var v string
		if err := s.Pool.QueryRow(ctx, `SELECT md5(jsonb_agg(to_jsonb(s) ORDER BY stream_id,version,observed_at)::text) FROM score_snapshots s`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := fingerprint()
	detail, expiry := policy.cutoffs()
	rows, err := s.Pool.Query(ctx, replayRetentionRows+`SELECT s.input->>'case',r.disposition
 FROM retention_rows r JOIN score_snapshots s USING(stream_id,observed_at,version)`, at, detail, expiry)
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]string{}
	for rows.Next() {
		var name, disposition string
		if err := rows.Scan(&name, &disposition); err != nil {
			t.Fatal(err)
		}
		actual[name] = disposition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, f := range fixtures {
		if actual[f.name] != f.disposition {
			t.Errorf("%s: got %s, want %s", f.name, actual[f.name], f.disposition)
		}
	}
	for _, tc := range []struct {
		name                    string
		detail, outer           time.Duration
		retained, expired, thin int64
	}{
		{"tiered", policy.Detail, policy.Retention, 7, 2, 4},
		{"disabled", policy.Detail, 0, 13, 0, 0},
		{"legacy expiry", 0, policy.Retention, 11, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := ReplayRetentionPolicy{AsOf: at.In(time.FixedZone("offset", 2*60*60)), Detail: tc.detail, Retention: tc.outer}
			r, err := s.PreviewReplayRetention(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if r.Total.Snapshots != 13 || r.Retained.Snapshots != tc.retained || r.Expired.Snapshots != tc.expired || r.Thinnable.Snapshots != tc.thin || r.StreamVersions != 6 || r.FutureRows != 1 {
				t.Fatalf("wrong counts: %+v", r)
			}
			if r.AsOf.Location() != time.UTC || !r.AsOf.Equal(at) || r.MeasuredAt.IsZero() || r.Total.PayloadBytes <= 0 || r.Total.PayloadBytes != r.Retained.PayloadBytes+r.Expired.PayloadBytes+r.Thinnable.PayloadBytes {
				t.Fatal("time or payload accounting differs")
			}
			if r.Storage.TotalAllocatedBytes != r.Storage.MainTableBytes+r.Storage.MainIndexBytes+r.Storage.TOASTBytes {
				t.Fatal("physical allocation categories overlap")
			}
		})
	}
	if fingerprint() != before {
		t.Fatal("preview changed replay rows")
	}
	var scoreRows, migrations int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stream_scores),(SELECT count(*) FROM schema_migrations)`).Scan(&scoreRows, &migrations); err != nil || scoreRows != 0 || migrations != beforeMigrations {
		t.Fatalf("preview changed scores or schema: %d %d %v", scoreRows, migrations, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.PreviewReplayRetention(cancelled, policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preview: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `TRUNCATE score_snapshots`); err != nil {
		t.Fatal(err)
	}
	r, err := s.PreviewReplayRetention(ctx, policy)
	if err != nil || r.Total.Snapshots != 0 || r.Total.PayloadBytes != 0 || r.StreamVersions != 0 {
		t.Fatalf("empty archive: %+v %v", r, err)
	}
}
