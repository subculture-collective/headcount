# Raw measurement retention and chart rollups

Status: assessment complete for #17. Keep the current raw measurements and
collection cadence. No expiry policy, rollup, or collector change is authorized
by this assessment. Revisit raw expiry after replay storage work and a separate
owner decision about historical research.

## Measured footprint and age

A read-only production inspection on November 9, 2026 at 15:27 UTC found:

| Hypertable | Allocated bytes including indexes/TOAST | Chunks | Compressed chunks | Oldest measurement UTC |
|---|---:|---:|---:|---|
| `viewer_samples` | 1,494,745,088 | 4 | 2 | October 19, 04:51 |
| `follower_samples` | 967,663,616 | 4 | 2 | October 19, 04:54 |
| `chat_minutes` | 2,202,042,368 | 4 | 2 | October 19, 04:51 |

Together these occupy about 4.34 GiB, with about 21 days of observations.
Newest viewer/follower observations were November 9, 15:27; chat was 15:26.
The configured seven-day compression policies retain the measurements; they
are not deletion policies. No raw-measurement retention policy was installed.
The job-statistics retention policy concerns Timescale's own job history.

A second metadata read grouped the twelve chunks across these three tables:

| Chunk interval UTC, end exclusive | Combined bytes | Chunks | All compressed |
|---|---:|---:|---|
| October 15–22 | 22,093,824 | 3 | yes |
| October 22–29 | 76,947,456 | 3 | yes |
| October 29–November 5 | 2,454,126,592 | 3 | no |
| November 5–12 | 2,111,463,424 | 3 | no |

Chunk boundaries describe partitions, not the time of their first recorded
measurement. The older compressed chunks and newer uncompressed chunks cannot
be used as a linear daily-growth forecast. Counts and allocation can change
between these two reads because ingestion continued.

Use `hypertable_detailed_size` for table totals and `chunks_detailed_size` joined
to `timescaledb_information.chunks` for partition allocation, compression and
range metadata. Find actual oldest/newest timestamps with ordered `ts LIMIT 1`
queries in both directions. Read `timescaledb_information.jobs` for policy
configuration. Run these queries in `BEGIN READ ONLY` with a statement timeout;
never call `drop_chunks`, add a policy, or expose credentials or measurement
payloads during an inventory. These are allocated bytes, not predicted bytes
returned to the filesystem by a future policy.

## What callers require

This inventory follows source revision `1af2c76` and scoring version 11.

| Caller | Required interval or granularity | Consequence for retention |
|---|---|---|
| [`Store.LoadSeries`](../../internal/store/score.go) and [`score.Build`](../../internal/score/score.go) | Whole observed stream, `FirstSeenAt` through `LastSeenAt`, aligned by minute; initial event state also reads earlier observations | Keep every needed minute for open streams and any promised rescoring. Missing, unhealthy, restricted and contextual minutes must remain distinguishable. |
| Final scoring/backfill in `StreamsToScore` | All unfinished scoring work, oldest ended streams first, up to 200 per pass; no maximum age | A wall-clock cutoff can delete inputs before backfill reaches them. Protect unscored and older-version work explicitly. |
| Provisional scoring in `LiveStreamsToScore` | Open streams observed for at least 60 minutes, refreshed about every 15 minutes; no maximum stream length | A fixed TTL alone cannot protect long-running streams. A measured 47-hour maximum today is not a contractual bound. |
| Engagement peers in `ChatPeerStats` | Completed streams in the seven days before the target's start; one other stream per channel, matching metric version and source timestamp | Keep valid scalar peer metrics. Rebuilding a missing/stale metric still needs that source stream's raw inputs. A backfill's peer window is relative to its historical start, not today. |
| [`BehaviorPeers`](../../internal/store/behavior.go) and behavior comparisons | Previous seven days, matched language/category/audience; at most 200 references, including up to ten own-channel streams | Keep frozen references for replay and valid cached metrics for new scoring. Raw minute rollups cannot replace account/activity evidence. |
| Shared and young-account evidence | Target authors plus cross-channel observations from seven days before target start through its last observation; account-age threshold 90 days, new-account threshold 30 days | These age thresholds are not retention horizons. Per-author overlap and behavior cannot be reconstructed from count-only summaries. |
| [`API` stream charts and channel summaries](../../internal/api/api.go) | Entire requested stream; follower charts/gains start five minutes before first observation | Old charts and average/follower summaries would change or disappear unless archived before raw deletion. |
| Public recent views | Overview: 24-hour message count; live viewer lookup: last ten minutes of the stream; mini charts: three hours in ten-minute buckets | Keep these recent paths on raw data. Their short windows do not define historical scoring needs. |
| [`Research packets`](../../internal/research/packet.go) and [`persistence`](../../internal/research/persistence.go) | Self-contained full typed inputs; persistence carries state at most 30 minutes and splits on gaps/abstention | Export selected packets before replay expiry. An hourly score history cannot reconstruct the current 15-minute monitoring sequence. |

`Packet.Verify` rejects packets whose observed stream span exceeds seven days.
That offline validation limit does not constrain `LoadSeries` or live collection,
and must not be used to justify seven-day raw expiry.

At the inspection time, 39,761 finished streams still needed initial scoring or
an upgrade to v11. The oldest ended October 29 at 01:17 UTC. Open streams had
an observed span of up to 47 hours. `stream_chatter_summary` was empty. The
collector's unchanged defaults retain per-author detail for 30 days after stream
end and replay snapshots for seven days; neither is a raw-measurement policy.
Author summarization preserves denominators and selected aggregates, but the
scorer explicitly abstains from shared-account evidence after author pruning.
Therefore keeping minute samples alone does not promise complete historical
rescoring after related evidence expires.

## Candidate horizons and chart-only archives

| Candidate | Benefit | Limits and decision |
|---|---|---|
| Retain raw data with current compression | Preserves current readers and backfill | Recommended now. Measure after replay thinning/deduplication rather than deleting lower-volume evidence first. |
| Seven- or thirty-day raw TTL | Bounds storage sooner | Reject as an immediate policy: backlog, open-stream spans, historical peer windows and research needs are not protected by age alone. |
| Ninety days of minute detail plus older hourly charts | Possible later bounded history with useful old charts | A candidate for an owner decision, not a selected horizon or a sufficient guard by itself. Hold open, unscored, stale-version and approved research intervals, including their dependencies. |
| Slower collection or coarse inputs for scoring | Fewer incoming rows | Excluded: changes detector inputs, timing, coverage and burst semantics. Requires a separate model study. |

If an archive is approved, keep it on an explicit chart reader, never behind
`LoadSeries`. Bucket by stream and UTC hour, with source interval, schema version,
coverage counts and missingness. Preserve viewer sum/count and min/max; follower
first/last with their timestamps and min/max; and clearly named summed message
counts and summed minute-chatter counts. Keep ordered boundary values needed for
chart continuity. Preserve finalized per-stream chart summaries before deletion.

Summed minute-chatters are not distinct people over an hour. Do not label them
hourly unique chatters. Averages of bucket ratios do not recover paired-minute
engagement medians. Hourly summaries also lose burst timing, repeated-value
patterns, restriction transitions and outage structure. Health/coverage metadata
helps interpret a chart but does not recover those inputs. Label older charts
with their granularity and unavailable detail; retain missing buckets as gaps.

Do not activate a refresh or deletion policy as part of this design. A future
implementation must demonstrate that late observations/reopened streams update
the archive, and that refresh jobs cannot erase retained archives after their raw
source expires. Chunk deletion also needs an interval-level hold check: preserving
one stream's rows is incompatible with dropping a chunk that still contains them.

## Follow-up and release boundary

Finish the replay-retention preview, bounded pruning, maintenance diagnostics
and qualified rollout in roadmap #12 first. Keep raw samples, author retention
and collection cadence unchanged in those releases. Record another compressed
allocation/churn inventory after backfill and a complete compression cycle.

Then present the owner with a concrete proposal covering the desired historical
research/rescoring horizon, which cases need self-contained exports or raw-data
holds, historical chart availability, and a maximum open-stream strategy. Those
choices determine the raw horizon; ninety days is only a discussion candidate.
No new raw-expiry implementation issue is ready until that decision is recorded.

Any later implementation must prove chart parity on a disposable database,
weighted aggregate correctness, visible gaps, hold/chunk safety, late-data
refresh, frozen replay portability, and unchanged minute-based detector output
for every retained input. Require an exact deletion preview, consistent backup
with a verified restore, and approval of the concrete irreversible loss before
production activation. Restoring deleted raw history requires a retained backup;
disabling a policy does not recreate it.
