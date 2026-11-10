# Headcount

![Headcount repository banner](assets/brand/banner.png)

Headcount tracks Twitch channels once they reach the global top 200 and scores
each stream for patterns that view-bots, follow-bots and chat-bots produce. The
score is the sus quotient, from 0 to 100. A high quotient means the data has
patterns bots produce. It does not prove the streamer bought bots.

The public dashboard is read-only, at
[headcount.subcult.tv](https://headcount.subcult.tv/). Collection began in
October 2026, so scores are provisional and change often. The detector weights
are set by hand and are not calibrated.

## Features

- **Collector:** follows the top 200 every minute and keeps tracking every
  channel that has appeared there. It samples viewers, followers and anonymous
  chat, and scores each stream when it ends (and every 15 minutes for streams
  live over an hour).
- **Dashboard:** a leaderboard, live channels, channel, stream and category
  pages, an engagement map of audience size against chat, per-stream charts of
  viewers, chatters and follows, and channel and category search.
- **JSON API:** the read-only data behind the dashboard under `/api/`, rate
  limited per client.
- **Search-friendly pages:** each page is rendered on the server with its own
  title, description, canonical link and JSON-LD, plus `/robots.txt` and
  `/sitemap.xml`.
- **Research tools:** `cmd/research` exports frozen scoring inputs and replays,
  evaluates and calibrates them offline without changing public scores.
- **Backups:** a Compose service writes a daily `pg_dump`.

## What it collects

Once a channel enters the top 200 it is tracked from then on.

| Data | Source | Cadence |
|---|---|---|
| Top 200 live channels | Helix `Get Streams` | every minute |
| Viewer count, title, category for every tracked channel | Helix `Get Streams` (100 IDs per call) | every minute; cold tier every 10 minutes |
| Follower total | Helix `Get Channel Followers` (total only, app token) | live hot: 1 min, live tracked: 2 min, offline: hourly |
| Chat: messages, unique chatters, new chatters, repetition, bans | anonymous IRC | per minute, while live |
| Raids, chat modes (sub-only, emote-only, followers-only) | IRC notices | as they happen |
| Chatter account creation dates | Helix `Get Users` | new authors; omissions retried after 1–24 hours; found accounts refreshed after 7 days |
| Chat restrictions and Shared Chat sessions | public Helix endpoints, app token | about every 10 minutes |
| Commands, utility and bot-badge activity, timing bins | IRC aggregates | per minute |

Raw message text is never stored. Repetition is measured in memory per minute.
Per-chatter rows are replaced by a summary after `CHATTER_RETENTION_DAYS`.

Channels move between tiers:

- **hot:** in the top 200 this minute.
- **tracked:** has been in the top 200.
- **cold:** out of the top 200 for 7+ days with at least 5 scored streams and a
  channel quotient under 15. Polled every 10 minutes; chat is not joined. A
  cold channel returns to tracked if its quotient reaches 30, and to hot when it
  re-enters the top 200. Channels are never deleted.

## Scoring

Each detector rates one kind of evidence from 0 to 1:

| Component | Weight | Evidence |
|---|---|---|
| `periodic_spikes` | 0.6 | Upward viewer spikes that repeat at a fixed period and return to baseline |
| `synchronized_spikes` | 0.5 | Spikes that start in the same minutes as another channel's more often than chance (binomial test, Bonferroni-corrected; discounted when many channels spike together) |
| `spike_chat_lift` | 0.5 | Chat does not grow when viewers spike, comparing each unexplained spike with the minutes just before it |
| `chat_engagement` | 0.5 | Median chatters per minute per 1k viewers far below a peer baseline fitted on log(viewers), per language, adjusted for category |
| `follower_purge` | 0.4 | Large observed net follower losses; removal cause unknown |
| `chatter_accounts` | 0.4 | High share of creation dates under 30 days old; needs 80% date coverage |
| `shared_accounts` | 0.4 | Chatters with accounts under 90 days old who also chatted in two or more other tracked channels in the week before |
| `follow_bursts` | 0.3 | Observed net-change intervals above the usual follow rate |
| `unexplained_jumps` | 0.3 | Sharp viewer changes with no raid or category change nearby |
| `spike_follow_lift` | 0.25 | Follows do not speed up when viewers spike |
| `chat_repetition` | 0.15 | Share of chat matching the most repeated non-command message |
| `start_ramp` | 0.1 | Viewers arrive implausibly fast after going live |

Components that share a likely cause are grouped: audience shape, weak spike
response and low engagement form an **audience** group; repetition and account
features form **chat**; net follower anomalies form **follows**. Each group
contributes its strongest weighted component, and groups combine by noisy-OR,
each capped at 0.3 plus the strongest other group. One group alone scores at
most 30.

Each stream score also reports **observation coverage**: sampled viewer
minutes, healthy chat minutes, follower samples and resolved account dates.
Coverage is not detection accuracy. A stream with too few observations gets an
insufficient-evidence status instead of a score. Known context such as raids,
category changes, chat modes and Shared Chat suppresses the evidence it could
explain.

A channel's quotient is the coverage- and recency-weighted average of its last
ten streams, with a prior worth half a full-coverage stream at zero. Scores from
different `score.Version` values are not on a calibrated common scale. More
detail: [implementation coverage](docs/research-implementation.md) and the
[replay and evaluation workflow](docs/research-workflow.md).

## Quick start

You need Docker and a Twitch application (client ID and secret). The app uses
the client-credentials flow and needs no user authorization.

Create `.env` in the repository root with:

- `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`
- `POSTGRES_PASSWORD`: the database owner password
- `API_DB_PASSWORD`: the password for the read-only role the dashboard uses

Then:

```sh
docker compose up -d --build
curl -s localhost:18090/healthz     # collector health
open http://localhost:18091/        # dashboard
```

PostgreSQL (TimescaleDB) listens on `127.0.0.1:55432`. `compose.yaml` also
publishes the collector and API ports on a fixed LAN address; remove those
second `ports` entries if that address is not on your host.

### Tests

You need Go (version in `go.mod`) and a disposable TimescaleDB instance for the
database tests:

```sh
export HEADCOUNT_TEST_DATABASE_URL=postgres://...   # disposable database
go vet ./...
go test -p 1 -race ./...
# Optional real-browser map checks (Node 18+ and Chromium):
node scripts/check-engagement-map.mjs
WIDTH=390 THEME=light node scripts/check-engagement-map.mjs
```

Test packages run one at a time because their databases share a cluster-wide
role. The browser check runs against its own fixture server and uses no live
data.

## Configuration

Collector settings (environment variables, defaults in parentheses):

- `TOP_N` (200), `END_GRACE_MINUTES` (4), `COLD_EVERY_MINUTES` (10),
  `LOG_LEVEL`
- `SEED_LOGINS`: comma-separated channels to track before they reach the top
  200
- `CHATTER_RETENTION_DAYS` (30), `REPLAY_RETENTION_DAYS` (7),
  `REPLAY_DETAIL_HOURS`: retention for per-chatter rows and score snapshots;
  zero disables a retention job
- `REPLAY_CONTEXT_WRITES` (false): store score snapshots as references to
  shared replay context instead of inline
- `UTILITY_BOT_IDS`: comma-separated utility account IDs whose activity is
  treated as metadata
- `HEALTH_ADDR` (`:8080`), `DATABASE_URL`, `API_DB_PASSWORD`

API settings:

- `DATABASE_URL`: connects as the read-only `susq_api` role, which the
  collector enables from `API_DB_PASSWORD`
- `API_ADDR`, `RATE_PER_MINUTE` (120), `RATE_BURST` (60)
- `TRUSTED_PROXIES`: set to your reverse proxy's addresses so
  `X-Forwarded-For` is used

The database (`susq`), its roles and the backup files keep the project's
original name.

## Deploy

The Dockerfile builds both binaries into one image; the collector is the
default entrypoint and the API runs with `/usr/local/bin/api`. `compose.yaml`
runs TimescaleDB, the collector, the API and a backup service that writes a
daily `pg_dump -Fc` to `./backups` and keeps the two newest. Put a reverse
proxy in front of the API port and set `TRUSTED_PROXIES`. The API needs only
read access, so it can run anywhere that reaches the database.

## Project layout

```text
cmd/collector     discovery, polling, chat, follower sampling, scoring
cmd/api           read-only JSON API and dashboard
cmd/research      offline export, replay, evaluation and calibration
internal/helix    Helix client with a shared, prioritized rate-limit budget
internal/tracker  minute poller, stream sessions, tiers, follower sampler
internal/chat     IRC connection pool and per-minute aggregation
internal/detect   pure detector functions (tested on synthetic streams)
internal/score    loads streams, runs detectors, peer baseline, channel quotient
internal/store    Postgres/TimescaleDB access and migrations
web/static        dashboard (no build step)
```

Interface rules are in [docs/design-system.md](docs/design-system.md) and the
artwork in [assets/brand](assets/brand/README.md).

## Known limits

- Helix viewer counts are cached for several minutes, so spikes shorter than
  that are blurred or missed.
- Ad breaks, front-page placement, embeds and chat restrictions change these
  numbers for innocent reasons. Raids, category changes, chat modes and Shared
  Chat are considered; other distribution remains a confounder.
- A third party can view-bot a channel to get it penalized.
- Category families are a fixed list of non-game categories; every other
  category counts as a game. A mostly botted category can pull its own baseline
  down, up to the 1.5 SD cap.
- Shared accounts only sees message authors in tracked channels. Raid trains
  and friend groups also share chatters; the young-account filter reduces, but
  does not remove, that overlap.

## Contributing

- Report bugs and request features in
  [GitHub issues](https://github.com/subculture-collective/headcount/issues).
- Ask questions and share ideas in
  [GitHub Discussions](https://github.com/subculture-collective/headcount/discussions).
- Send code as a pull request on GitHub against `main`.

CI runs `go vet`, the unit and database integration tests with the race
detector, the build of every command and an API image check on every pull
request. The `Mirror CI gate` check must pass before a pull request can merge.
Bump `score.Version` when detector logic changes.

## License

Original contributions are licensed under the GNU General Public License,
version 3 or (at your option) any later version (`GPL-3.0-or-later`). See
[LICENSE](LICENSE). Third-party code, fonts, data and other assets keep their
own licenses and notices.
