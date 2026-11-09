package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestRetentionPreviewArguments(t *testing.T) {
	t.Setenv("DATABASE_URL", "invalid-secret-connection-url")
	for i, args := range [][]string{
		{}, {"-out", "unused"}, {"-as-of", "invalid", "-out", "unused"},
		{"-as-of", "2026-11-09T12:00:00Z", "-out", "unused", "-detail-window", "-1h"},
		{"-as-of", "2026-11-09T12:00:00Z", "-out", "unused", "-detail-window", "200h"},
		{"-as-of", "2026-11-09T12:00:00Z", "-out", "unused", "unexpected"},
		{"-as-of", "2026-11-09T12:00:00Z", "-out", "unused"},
	} {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			if err := retentionPreview(args); err == nil || strings.Contains(err.Error(), "invalid-secret-connection-url") {
				t.Fatalf("arguments or credentials were not protected: %v", err)
			}
		})
	}
}

func TestRetentionPreviewCLIIntegration(t *testing.T) {
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
	name := fmt.Sprintf("headcount_preview_%d", time.Now().UnixNano())
	if _, err := admin.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
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
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO score_snapshots VALUES(1,'2026-11-01',11,'{"private":"never-print-packet"}','{}'),(1,'2026-11-09',11,'{}','{}')`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", u.String())
	out := filepath.Join(t.TempDir(), "preview.json")
	args := []string{"retention-preview", "-as-of", "2026-11-09T12:00:00Z", "-out", out}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report store.ReplayRetentionPreview
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.Total.Snapshots != 2 || report.Expired.Snapshots != 1 || report.Retained.Snapshots != 1 || strings.Contains(string(b), "never-print-packet") {
		t.Fatal("CLI report contains wrong counts or private replay data")
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("report permissions: %v", err)
	}
	if err := run(args); err == nil {
		t.Fatal("preview overwrote an existing report")
	}
	after, err := os.ReadFile(out)
	if err != nil || string(after) != string(b) {
		t.Fatal("existing report changed")
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM score_snapshots`).Scan(&n); err != nil || n != 2 {
		t.Fatal("CLI preview changed database rows")
	}
}
