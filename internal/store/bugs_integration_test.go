package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func bugTestStore(t *testing.T) *Store {
	t.Helper()
	raw := os.Getenv("HEADCOUNT_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("HEADCOUNT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("headcount_bugs_%d", time.Now().UnixNano())
	if _, err = admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	s, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewContextInvalidatesInflightScoreIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Hour)
	_, err := s.Pool.Exec(ctx, `INSERT INTO channels(id,login,display_name,first_seen_at) VALUES(1,'fixture','Fixture',$1);
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at,ended_at) VALUES(10,1,$1,$1,$1::timestamptz+interval '2 hours',$1::timestamptz+interval '2 hours');`, pgx.QueryExecModeSimpleProtocol, at)
	if err != nil {
		t.Fatal(err)
	}
	sc := Score{StreamID: 10, ChannelID: 1, LastSeenAt: at.Add(2 * time.Hour), ObservedAt: time.Now().Add(-time.Minute), Final: true, Version: 11,
		Peer: &PeerStat{MedianViewers: 1000, ChattersPerK: 2}}
	if err = s.SaveScore(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordDistributionContext(ctx, 10, at, at.Add(time.Hour), "promotion", "test annotation"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveScore(ctx, sc); !errors.Is(err, ErrStaleScore) {
		t.Fatalf("in-flight score did not reject stale context: %v", err)
	}
	var pending bool
	if err = s.Pool.QueryRow(ctx, `SELECT scored_at IS NULL AND NOT EXISTS(SELECT 1 FROM stream_peer_metrics WHERE stream_id=10) FROM streams WHERE id=10`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("new annotation no longer pending rescoring")
	}
}

func TestStreamBoundaryOwnershipIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	_, err := s.Pool.Exec(ctx, `INSERT INTO channels(id,login,display_name,first_seen_at,tier) VALUES(1,'fixture','Fixture',$1,'cold');
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at) SELECT i,1,$1,$1,$1::timestamptz+interval '2 minutes' FROM generate_series(10,11) i;`, pgx.QueryExecModeSimpleProtocol, at)
	if err != nil {
		t.Fatal(err)
	}
	healthy := true
	for i, sid := range []int64{10, 11} {
		if err = s.RecordChat(ctx, at.Add(time.Duration(i)*time.Minute), []ChatMinute{{ChannelID: 1, StreamID: sid, Messages: 1, UniqueChatters: 1, Healthy: &healthy}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	ser, err := s.LoadSeries(ctx, StreamRef{ID: 10, ChannelID: 1, FirstSeenAt: at, LastSeenAt: at.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(ser.Chat) != 1 {
		t.Errorf("other stream's chat leaked into scoring: %d rows", len(ser.Chat))
	}
	// An accepted room-minute cannot authorize author rows for another stream.
	err = s.RecordChat(ctx, at.Add(2*time.Minute), []ChatMinute{{ChannelID: 1, StreamID: 10, Messages: 1, Healthy: &healthy}}, []ChatterMinute{{ChannelID: 1, StreamID: 11, UserID: 99, Messages: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM stream_chatters WHERE user_id=99`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("room-minute authorized a chatter in another stream")
	}
	open, err := s.OpenStreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(open[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["Tier"] != "cold" {
		t.Errorf("restart loses cold tier: %v", fields)
	}
}

func TestReopenedStreamInvalidatesFinalScoreIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
	_, err := s.Pool.Exec(ctx, `INSERT INTO channels(id,login,display_name,first_seen_at) VALUES(1,'fixture','Fixture',$1);
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at,ended_at) VALUES(10,1,$1,$1,$1,$1);`, pgx.QueryExecModeSimpleProtocol, at)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveScore(ctx, Score{StreamID: 10, ChannelID: 1, LastSeenAt: at, Version: 10, Final: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordLive(ctx, at.Add(time.Minute), []LiveSample{{StreamID: 10, ChannelID: 1, Login: "fixture", Display: "Fixture", StartedAt: at, Viewers: 1000}}); err != nil {
		t.Fatal(err)
	}
	var final bool
	if err = s.Pool.QueryRow(ctx, `SELECT final FROM stream_scores WHERE stream_id=10`).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if final {
		t.Fatal("reopened live stream still exposes a final score")
	}
	if err = s.RecordLive(ctx, at.Add(2*time.Minute), []LiveSample{{StreamID: 11, ChannelID: 1, Login: "fixture", Display: "Fixture", StartedAt: at.Add(2 * time.Minute), Viewers: 1000}}); err != nil {
		t.Fatal(err)
	}
	var open int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM streams WHERE channel_id=1 AND ended_at IS NULL`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Fatalf("new stream left the previous stream open: %d open rows", open)
	}
}

func TestFollowerSamplesUseObservationTimeIntegration(t *testing.T) {
	s := bugTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute)
	var sample FollowerSample
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"ChannelID":1,"Total":200,"ObservedAt":%q}`, at.Add(time.Minute+20*time.Second).Format(time.RFC3339))), &sample); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFollowerSamples(ctx, at, []FollowerSample{sample}); err != nil {
		t.Fatal(err)
	}
	var observed time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT ts FROM follower_samples WHERE channel_id=1`).Scan(&observed); err != nil {
		t.Fatal(err)
	}
	if !observed.Equal(at.Add(time.Minute)) {
		t.Fatalf("delayed observation backdated: %v", observed)
	}
}
