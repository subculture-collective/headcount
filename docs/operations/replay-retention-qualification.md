# Replay retention qualification ledger

Scope: qualification evidence and release gates. This documentation does not
enable production thinning. Issue #24 stays open until its concrete operator
checkpoint and live rollout are complete. Current gate results are recorded on
the PR. The [runbook](replay-retention-rollout.md) defines that boundary.

## Fresh consistent backup

All eighteen source application-table counts and replay COPY streams used one
exported repeatable-read snapshot. The main archive contains schema, catalog and
all other table data; four compressed streams carry complete replay rows. No
production schema, retention horizon or collector configuration changed.

Backup completed at `2026-11-09T17:41:54.689514+00:00`: **93,142,876,445 bytes**,
including **573,290 replay rows**. Archive metadata records
the exact source image. The independent full restore uses an isolated matching database with no
published ports. Its final count comparison must follow
`timescaledb_post_restore()`; the completed comparison is recorded on the PR.
Archive completion and contents alone are not restore proof.

| Archive component | Bytes | SHA-256 |
|---|---:|---|
| database-without-replay-data.dump | 1,374,168,853 | `cdf90b65312f55bd3f5f6d62154c4aa9c52a9f4cd02e3ab3e91821dda4087ff5` |
| replay-0.copy.zst | 21,506,108,444 | `bb54dd112bd3840ccbdcb95cf5a275aa4a0d5a0edf621d4312282365544f3385` |
| replay-1.copy.zst | 17,905,382,927 | `9867a34b0b193cc4d13f27f1c8a8dbd3022d5f250e7ade0f61da3a9e638c400d` |
| replay-2.copy.zst | 23,844,875,745 | `598dd1662538c35e1c4ffea6f5a964fb78ec6ae2b0e8112b4f8d63043b6874ee` |
| replay-3.copy.zst | 28,512,340,476 | `c304eafec6bc21158fe7644c8622d7eab01c1b39500ba25bbdd96d23ffb247c9` |

| Source application table | Snapshot count |
|---|---:|
| channel_events | 2,331,269 |
| channels | 6,925 |
| chat_minutes | 19,642,418 |
| follower_samples | 14,674,302 |
| replay_contexts | 0 |
| schema_migrations | 9 |
| score_snapshot_contexts | 0 |
| score_snapshots | 573,290 |
| spike_onsets | 144,940 |
| stream_behavior_metrics | 63,903 |
| stream_chatter_summary | 0 |
| stream_chatters | 37,034,802 |
| stream_peer_metrics | 23,786 |
| stream_scores | 63,903 |
| streams | 64,325 |
| twitch_users | 8,741,639 |
| user_observations | 15,615,464 |
| viewer_samples | 19,705,049 |

## Protected research exports

The private export manifest contains **602 verified
self-contained packets**, totaling **784,643,236 bytes**. Selection
includes all stored snapshots for the five owner-nominated channels, a deterministic
30-stream sample and 15 quiet-chat challenge selections, deduplicated by identity.
These are research selections, not ground-truth labels. No existing local research
export pool was found in the worktree or native checkout; the operator can name
additional externally held cases before approval.

The real CLI exported referenced synthetic evidence, the bounded pruner expired
its source snapshot, GC removed both contexts with no dangling reference, and the
packet replayed with the disposable database removed and networking disabled.
That conformance check is separate from the fresh full-restore qualification.

## Proposed fixed-time preview

At `2026-11-09T17:28:38.613590+00:00`, one read-only repeatable-read transaction using the
shared source selector recorded:

| Disposition | Snapshots | Referenced input/output bytes |
|---|---:|---:|
| Retained | 209,987 | 49,831,544,015 |
| Expired by outer window | 116 | 35,451,773 |
| Older intermediate, thinnable | 363,857 | 102,227,363,250 |

The private candidate identity manifest hashes to
`91fbbb9e313c31783e90b8c453a601f582ef0170addc04a39b1b844ef9b91ee9`. The preview must be refreshed after qualification
and before the operator checkpoint. Moving cutoffs and new observations change
later candidates. Referenced payload is not allocated-file shrink.

## Release gates

Before activation, require green checks on the final PR head, all eighteen
restored counts matching, bounded deletion/retained-identity conformance on the
copy, and exact-image candidate/retained-rollback startup rehearsals. Record their
sanitized results in the PR qualification record alongside this fixed backup
and preview ledger.
Only then request approval of the refreshed preview and recovery limits.

Native activation must preserve the API, database and backup baseline captured
after normal source promotion. Keep referenced writers and raw/author retention
unchanged. Production maintenance settings are not automatically applied: review
observed churn and existing thresholds first. Disabling further thinning or
rolling back the binary cannot recreate deleted evidence.

Private archive files, packets, manifests and detailed logs stay under
`/home/onnwee/.local/state/headcount/wave3-20261109T165313Z/`, outside Git.
