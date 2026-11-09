package helix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A drained bucket with no known reset time must schedule a refill instead of
// waiting forever for a rate-limit header.
func TestAcquireRecoversWithoutHeaders(t *testing.T) {
	c := New("id", "secret")
	c.remaining = 0

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.acquire(ctx, PriorityHigh); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire from empty bucket: %v, want deadline", err)
	}
	c.budgetMu.Lock()
	reset := c.resetAt
	c.budgetMu.Unlock()
	if until := time.Until(reset); until < 50*time.Second || until > time.Minute {
		t.Fatalf("reset scheduled in %v, want about a minute", until)
	}

	// Once the scheduled reset passes, the bucket refills.
	c.budgetMu.Lock()
	c.resetAt = time.Now().Add(-time.Second)
	c.budgetMu.Unlock()
	if err := c.acquire(context.Background(), PriorityLow); err != nil {
		t.Fatalf("acquire after reset: %v", err)
	}
}

func TestRefundCapsAtLimit(t *testing.T) {
	c := New("id", "secret")
	c.refund()
	if c.remaining != c.limit {
		t.Fatalf("remaining %d exceeds limit %d", c.remaining, c.limit)
	}
}

func TestBudgetIgnoresOutOfOrderResponses(t *testing.T) {
	c := New("id", "secret")
	reset := time.Now().Add(time.Minute).Truncate(time.Second)
	c.remaining = 100
	c.resetAt = reset
	for _, tt := range []struct {
		name      string
		remaining int
		reset     time.Time
	}{
		{"older request in same window", 200, reset},
		{"previous window", 700, reset.Add(-time.Minute)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{"Ratelimit-Remaining": {fmt.Sprint(tt.remaining)}, "Ratelimit-Reset": {fmt.Sprint(tt.reset.Unix())}, "Ratelimit-Limit": {"800"}}
			c.updateBudget(h)
			if c.remaining != 100 || !c.resetAt.Equal(reset) {
				t.Fatalf("late response restored spent quota: remaining=%d reset=%v", c.remaining, c.resetAt)
			}
		})
	}
}

func TestFirstBudgetResponseKeepsInflightReservations(t *testing.T) {
	c := New("id", "secret")
	c.remaining = 790
	h := http.Header{"Ratelimit-Remaining": {"799"}, "Ratelimit-Reset": {fmt.Sprint(time.Now().Add(time.Minute).Unix())}}
	c.updateBudget(h)
	if c.remaining != 790 {
		t.Fatalf("first response released in-flight reservations: %d", c.remaining)
	}
}

func TestTopStreamsRejectsInvalidLimit(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invalid limit panicked: %v", r)
		}
	}()
	if _, err := New("id", "secret").TopStreams(context.Background(), -1); err == nil {
		t.Fatal("accepted negative stream limit")
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestContextContractsAndUnknownSettings(t *testing.T) {
	c := New("app", "unused")
	c.token = "test"
	c.tokenExpiry = time.Now().Add(2 * time.Hour)
	body := `{"data":[{"follower_mode":true,"follower_mode_duration":10,"subscriber_mode":false,"emote_mode":false,"slow_mode":false}]}`
	c.http = &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("broadcaster_id") != "1" {
			t.Error("missing broadcaster ID")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	settings, err := c.ChatSettings(context.Background(), "1")
	if err != nil || !settings.FollowerMode || settings.FollowerMinutes == nil || *settings.FollowerMinutes != 10 {
		t.Fatalf("settings: %+v %v", settings, err)
	}
	body = `{"data":[]}`
	if _, err := c.ChatSettings(context.Background(), "1"); err == nil {
		t.Fatal("empty settings became unrestricted chat")
	}
	if session, err := c.SharedChat(context.Background(), "1"); err != nil || session != nil {
		t.Fatal("empty shared session not inactive")
	}
	body = `{"data":[{"session_id":"session","participants":[{"broadcaster_id":"1"},{"broadcaster_id":"2"}]}]}`
	session, err := c.SharedChat(context.Background(), "1")
	if err != nil || session.ID != "session" || len(session.Participants) != 2 {
		t.Fatalf("shared chat: %+v %v", session, err)
	}
}
