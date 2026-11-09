package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"git.subcult.tv/subculture-collective/headcount/internal/research"
	"git.subcult.tv/subculture-collective/headcount/internal/store"
)

func retentionPreview(args []string) error {
	f := flag.NewFlagSet("retention-preview", flag.ContinueOnError)
	asOf := f.String("as-of", "", "fixed policy time (RFC3339Nano, required)")
	detail := f.Duration("detail-window", 48*time.Hour, "full replay window; zero disables thinning")
	outer := f.Duration("retention-window", 7*24*time.Hour, "outer replay window; zero disables pruning")
	out := f.String("out", "", "new private aggregate report (never overwritten)")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *out == "" || *asOf == "" {
		return errors.New("retention-preview requires -as-of and -out, with no positional arguments")
	}
	at, err := time.Parse(time.RFC3339Nano, *asOf)
	if err != nil {
		return errors.New("-as-of must be an RFC3339Nano timestamp")
	}
	policy := store.ReplayRetentionPolicy{AsOf: at, Detail: *detail, Retention: *outer}
	if err := policy.Validate(); err != nil {
		return err
	}
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL with private replay read access is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		// Configuration/connection errors can include the original connection URL.
		return errors.New("cannot open replay database; check DATABASE_URL and database access")
	}
	defer db.Close()
	report, err := db.PreviewReplayRetention(ctx, policy)
	if err != nil {
		return err
	}
	return research.WriteNew(*out, report)
}
