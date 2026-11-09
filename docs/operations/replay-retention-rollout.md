# Native replay retention rollout

This release activates 48 hours of detailed replay history within the existing
seven-day outer window. It retains the newest as-of replay independently per
stream/scoring version inside the outer window; older outer-expired rows remain
due even if they are a group's last row. Future observations do not displace
as-of evidence. It does not change model version, weights, raw minute cadence,
author retention or referenced-context writer activation.

## Evidence and irreversible checkpoint

Private evidence lives in
`/home/onnwee/.local/state/headcount/wave3-20261109T165313Z/`. Keep packet files,
archive data, IDs and credentials outside Git. The completed qualification
summary on the PR identifies the exact source/image, fixed-as-of preview,
protected exports, archive hashes, all-table restore comparison and candidate /
retained-rollback startup rehearsal. An aggregate payload estimate is not a
filesystem shrink promise.

Before activation, the operator must approve that concrete preview and its
recovery-point limits. The checkpoint covers ongoing 48-hour/seven-day retention,
not only one historical batch. New observations and moving cutoffs change later
delete candidates. Preserve additional research cases by export before approval.
Do not infer approval from a passing CI run or the existence of this runbook.

## Qualification order

1. Confirm exact PR-head CI and an immutable candidate image. Compare packaged
   migrations against the nine admitted native hashes. The runbook's normal
   main promotion may refresh the API; take the collector-activation baseline
   after that API promotion has finished.
2. Capture collector/API/database/backup IDs, images, start times, native runtime
   and environment hashes. Preserve the pre-existing native checkout and staged
   Compose work. Resolve configuration privately; never print expanded secrets.
3. Export all stored snapshots for the five owner-nominated channels plus the
   deterministic 30-stream sample and 15 quiet-chat challenge selections. These
   are research selections, not bot labels. The manifest records each packet's
   input digest, file hash, size and selected identity. No existing local research
   export pool was found at qualification; review any additional external cases.
4. Use a fresh repeatable-read exported snapshot for all eighteen application
   counts, the main logical archive and four compressed replay COPY streams.
   Independently restore all streams and the Timescale catalog into an isolated
   matching database. Compare counts only after timescaledb_post_restore().
   Verify checksums and observe role restrictions; keep the backup immutable.
5. Rehearse the exact candidate and retained rollback collector against the copy,
   using dummy provider credentials on its internal network. Measure actual
   bounded pruning work, retained/latest/version boundaries and unchanged raw /
   author measurements separately from score/backfill writes. Replay protected
   exports after fixture expiry. No real provider call belongs to this rehearsal.
6. Produce the fixed-time native preview and private identity manifest, review
   the selected exports and restore proof, then obtain the operator checkpoint.

## Activation and measurement

After approval, use the native project at `/home/onnwee/Work/Subcult/headcount`,
its existing private environment and Compose files, and the retained fleet
collector override. A release-specific override changes only the collector image
and adds REPLAY_DETAIL_HOURS=48 and REPLAY_RETENTION_DAYS=7. Compare the privately
resolved service to the running collector before replacing it. Do not rebuild
from the old native checkout or use an unqualified mutable image tag.

Recreate only service collector with `--no-deps --no-build --pull never`. Keep the
API, database and backup IDs/configuration fixed across this activation step.
The bounded worker starts independently of scoring backfill. Transactions examine
at most 500 snapshot identities; context cleanup has its own bounded cursor and
budget. These are transaction limits, not a promise to finish a large sweep in
one interval. Capture aggregate scanned/deleted counters and batch/cycle durations.

Check the collector health endpoint, current v11/v2 writes, viewer/chat/follower
sample advancement and a newly selected public stream. Compare retained
boundaries and per-version latest observations with the policy. Compare fixed
historical raw/author ranges or row fingerprints; live table counts grow normally.
Check API-role denial and native credential/configuration hashes. Record referenced
payload removed, dead/reusable estimates and allocated relation/TOAST/index bytes
as distinct measurements. Continue until the initial sweep's policy invariants
hold, rather than treating process startup as completion.

Keep global autovacuum enabled. Review the [maintenance evidence](replay-maintenance.md)
before changing replay-only settings. The initial deletion may already exceed
native thresholds; a more aggressive setting is not automatically justified by a
small synthetic benchmark. Any chosen table settings need their own recorded
before/after readback and rollback values. No VACUUM FULL, pg_repack, global tuning
or blocking rewrite is authorized by this release.

## Stop and recovery

Setting REPLAY_DETAIL_HOURS=0 stops further intermediate thinning; setting
REPLAY_RETENTION_DAYS=0 stops replay pruning and context cleanup. Configuration
changes require the scoped collector restart. The retained old collector can
still write inline snapshots under the additive schema. Keep its image and the
resolved collector-only rollback override. Verify rollback startup against the
restored copy before activation.

Neither binary/config rollback nor ordinary vacuum recreates deleted history.
Research packets remain portable without the database; other recovery needs the
retained backup and would discard observations after its recovery point. A live
database restore therefore requires a separate decision. Leave additive schema
and API admission unchanged during collector rollback. Record live verification
and recovery limits in the canonical Kvant operations report, and remove only
owned temporary containers/volumes/networks after qualification.
