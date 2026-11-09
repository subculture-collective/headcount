package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/store"
	"git.subcult.tv/subculture-collective/headcount/web"
	"github.com/jackc/pgx/v5"
)

// The map must retain measurements when the scorer has no independent peers.
func TestEngagementWithoutScorerBaseline(t *testing.T) {
	raw := os.Getenv("HEADCOUNT_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("HEADCOUNT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("headcount_map_test_%d", time.Now().UnixNano())
	if _, err := admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	st, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = st.Pool.Exec(ctx, `
 INSERT INTO channels(id,login,display_name,first_seen_at) VALUES(1,'example','Example',now());
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at) SELECT i,1,now()-interval '2 hours',now()-interval '2 hours',now() FROM generate_series(1,3) i;
 INSERT INTO stream_scores(stream_id,channel_id,scored_at,version,minutes,quotient,confidence,components)
 SELECT i,1,now(),9,60,0,0.5,'{"chat_engagement":{"available":false,"reason":"no independent past peers"}}'::jsonb FROM generate_series(1,3) i;
 INSERT INTO stream_peer_metrics(stream_id,metric_version,source_last_seen,language,game_id,game_name,median_viewers,chatters_per_k)
 VALUES(1,1,now(),'en','1','Example',500,12), (2,99,now(),'en','1','Example',900,3);
 `)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st.Pool, web.FS(), slog.New(slog.DiscardHandler))
	w := get(t, s.Handler(), "/api/engagement?days=7")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var data struct {
		Points []engagementPoint        `json:"points"`
		Fits   map[string]engagementFit `json:"fits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Points) != 1 {
		t.Fatalf("want only supported measurements, got %+v", data.Points)
	}
	p := data.Points[0]
	if p.StreamID != "1" || p.MedianViewers != 500 || p.ChattersPerK != 12 || p.ExpectedPerK != nil || p.Z != nil {
		t.Fatalf("measurement or unavailable comparison changed: %+v", p)
	}
	if len(data.Fits) != 0 {
		t.Fatal("small sample invented a reference fit")
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO chat_minutes(ts,channel_id,stream_id,messages,unique_chatters,new_chatters,first_msgs,emote_only,distinct_texts,top_text_count,bans,timeouts,healthy)
	 VALUES(now()-interval '1 hour',1,2,1,1,0,0,0,1,1,0,0,true),
	 (now()-interval '45 minutes',1,NULL,1,1,0,0,0,1,1,0,0,false)`); err != nil {
		t.Fatal(err)
	}
	w = get(t, s.Handler(), "/api/streams/1")
	var streamData struct {
		Chat [][]any `json:"chat"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &streamData); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(streamData.Chat) != 0 {
		t.Fatal("public stream API leaked chat owned by another stream")
	}
	// New summaries remain available independently of a scorer comparison;
	// historical measurements keep explicit null summaries.
	_, err = st.Pool.Exec(ctx, `
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at) VALUES(4,1,now()-interval '3 hours',now()-interval '3 hours',now());
 INSERT INTO stream_scores(stream_id,channel_id,scored_at,version,minutes,quotient,confidence,components)
 VALUES(4,1,now()-interval '1 hour',10,60,0,0.5,'{"chat_engagement":{"available":false,"values":{"median_viewers":500,"chatters_per_k":0,"aggregate_per_k":3.333,"eligible_minutes":60,"silent_minutes":40,"eligible_messages":200}}}');`)
	if err != nil {
		t.Fatal(err)
	}
	s.cache = nil
	w = get(t, s.Handler(), "/api/engagement?days=7")
	var payload struct {
		Points    []engagementPoint `json:"points"`
		Truncated bool              `json:"truncated"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(payload.Points) != 2 {
		t.Fatalf("summary endpoint: %d %s", w.Code, w.Body.String())
	}
	old, newer := payload.Points[0], payload.Points[1]
	if old.AggregatePerK != nil || newer.AggregatePerK == nil || *newer.AggregatePerK != 3.333 || newer.EligibleMinutes == nil || *newer.EligibleMinutes != 60 || newer.ExpectedPerK != nil {
		t.Fatalf("legacy/null summary or peer-independent metrics changed: %+v %+v", old, newer)
	}
	// Fetch one extra row to distinguish an exact cap from truncation.
	_, err = st.Pool.Exec(ctx, `
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at) SELECT i,1,now(),now(),now() FROM generate_series(100,3097) i;
 INSERT INTO stream_peer_metrics(stream_id,metric_version,source_last_seen,language,game_id,game_name,median_viewers,chatters_per_k)
 SELECT i,1,now(),'en','1','Example',500,12 FROM generate_series(100,3097) i;
 INSERT INTO stream_scores(stream_id,channel_id,scored_at,version,minutes,quotient,confidence,components)
 SELECT i,1,now(),10,60,0,0.5,'{}'::jsonb FROM generate_series(100,3097) i;`)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []bool{false, true} {
		if extra {
			_, err = st.Pool.Exec(ctx, `INSERT INTO stream_peer_metrics(stream_id,metric_version,source_last_seen,language,game_id,game_name,median_viewers,chatters_per_k)
    VALUES(3,1,now(),'en','1','Example',500,12)`)
			if err != nil {
				t.Fatal(err)
			}
		}
		s.cache = nil
		w = get(t, s.Handler(), "/api/engagement?days=7")
		var capped struct {
			Points    []engagementPoint `json:"points"`
			Truncated bool              `json:"truncated"`
			Sample    struct {
				Oldest   *time.Time `json:"oldest_scored_at"`
				Newest   *time.Time `json:"newest_scored_at"`
				Channels int        `json:"channels"`
			} `json:"sample"`
			Fits map[string]engagementFit `json:"fits"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &capped); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(capped.Points) != engagementLimit || capped.Truncated != extra || capped.Sample.Oldest == nil || capped.Sample.Newest == nil || capped.Sample.Channels != 1 || len(capped.Fits) != 0 {
			t.Fatalf("cap metadata incorrect, extra=%v: status=%d points=%d truncated=%v sample=%+v fits=%v", extra, w.Code, len(capped.Points), capped.Truncated, capped.Sample, capped.Fits)
		}
	}
	t.Run("category ranks before limit", func(t *testing.T) {
		_, err := st.Pool.Exec(ctx, `INSERT INTO channels(id,login,display_name,first_seen_at) SELECT i,'cap'||i,'Cap '||i,now() FROM generate_series(2,502) i;
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at,ended_at,game_id,game_name) SELECT 10000+i,i,now(),now(),now(),now(),'17','Cap category' FROM generate_series(2,502) i;
 INSERT INTO stream_scores(stream_id,channel_id,scored_at,version,minutes,quotient,confidence,components,final) SELECT 10000+i,i,now(),11,60,0,0.5,'{}',true FROM generate_series(2,502) i;`)
		if err != nil {
			t.Fatal(err)
		}
		// Put the highest score on the channel the old unordered limit excludes.
		_, err = st.Pool.Exec(ctx, `WITH omitted AS(SELECT c.id FROM channels c WHERE c.id BETWEEN 2 AND 502
 AND c.id NOT IN(SELECT c.id FROM streams st JOIN channels c ON c.id=st.channel_id WHERE st.game_id='17' GROUP BY c.id LIMIT 500) LIMIT 1)
 UPDATE stream_scores SET quotient=90 WHERE channel_id=(SELECT id FROM omitted)`)
		if err != nil {
			t.Fatal(err)
		}
		w := get(t, s.Handler(), "/api/categories/17")
		var category struct {
			Channels []struct {
				Max float64 `json:"max_quotient"`
			} `json:"channels"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &category); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(category.Channels) != 500 || category.Channels[0].Max != 90 {
			t.Fatalf("highest category score omitted at cap: status=%d channels=%d", w.Code, len(category.Channels))
		}
	})

}
