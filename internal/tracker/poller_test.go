package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/live"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestInitRestoresNewestStreamAndTier(t *testing.T) {
	raw := os.Getenv("HEADCOUNT_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("HEADCOUNT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("headcount_restart_%d", time.Now().UnixNano())
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
	s, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO channels(id,login,display_name,first_seen_at,tier) VALUES(1,'fixture','Fixture',now(),'cold');
 INSERT INTO streams(id,channel_id,started_at,first_seen_at,last_seen_at)
 VALUES(10,1,now()-interval '2 hours',now()-interval '2 hours',now()-interval '1 hour'),
 (11,1,now()-interval '30 minutes',now()-interval '30 minutes',now());`)
	if err != nil {
		t.Fatal(err)
	}
	ls := live.New()
	p := New(Config{ColdEvery: 10}, nil, s, ls, slog.New(slog.DiscardHandler))
	if err = p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	st, ok := ls.Get(1)
	if !ok || st.StreamID != 11 || st.Tier != "cold" {
		t.Fatalf("wrong restored session: %+v", st)
	}
	var open int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM streams WHERE ended_at IS NULL`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Fatalf("orphaned open session survived restart: %d", open)
	}
}
