# Native referenced-replay writer rollout

This is the second replay-storage release, following the qualified 48-hour detail /
seven-day outer retention policy in #24. `REPLAY_CONTEXT_WRITES=true` changes only
new private snapshot storage. Scorer v11, peer metric v2 and self-contained packet
schema 1 keep their existing meanings. Retained inline rows are not rewritten.

The writer factors nonempty `peers` and `behavior_peers` under
[ADR 001](../adr/001-immutable-replay-context.md). Per-stream evidence, partners,
empty arrays and nulls stay inline. A snapshot, full-input digest, immutable
contexts, bindings and score/cache updates commit together. Identity retries
compare complete evidence across formats; GC races retry the complete transaction.

## Qualification and operator checkpoint

Before native activation, record the exact green source head, immutable image ID,
public build marker after normal main promotion, and nine admitted migration
hashes. This release adds no migration and changes no API admission registration.
Read back private-table denial and existing read-only API privileges.

Use the tiered-retention baseline rather than comparing against the earlier
unthinned population. Capture a fresh consistent full eighteen-table backup,
transaction recovery point, component sizes/hashes and an independent matching
PostgreSQL/Timescale restore. Compare every application count after
`timescaledb_post_restore()`; bounded packet fixtures do not replace full restore.

Compare actual frozen current-v11 observations through inline and referenced
writers on the same disposable cluster and compression settings. Select streams
without conditioning on repeated observations, include every retained observation
per selected stream, and report the sampling frame, singletons and context reuse.
Keep older control/challenge cohorts separate. If new payload and backup costs do
not justify activation for the current workload, leave the feature disabled and
record that result.

On the full restored copy, run the exact candidate collector with dummy provider
credentials on an internal network. Prove new version-2 storage rows, canonical
reuse, frozen-input/stored-output parity and exports alongside unchanged inline
evidence. Exercise whole-transaction context/GC races through the database-backed
suite. Stop owned writers, take a consistent full mixed-format backup, restore
all eighteen tables independently, and compare counts plus old/new exported
packet identities, input hashes and replay output. Export packets through the
actual compatible CLI and replay without database/network access.

Start the retained tiered collector against the mixed-format copy and prove it
can continue inline writes. A health response with dummy credentials proves
startup/schema compatibility only; live provider qualification follows activation.
Retain both tested images and recovery overrides. Record changed fixture metadata
separately from source/raw data and remove only owned temporary resources.

The operational PR must contain the sanitized completed evidence and exact
activation/recovery plan before requesting the operator checkpoint. The checkpoint
in #25 covers new referenced writes and context cleanup. The already approved
48-hour/seven-day policy continues; no repeat retention approval is needed.

## Scoped activation

Use the native project `/home/onnwee/Work/Subcult/headcount`, its existing private
`.env` and Compose files, and the retained fleet/retention overrides. Preserve its
existing staged Compose work and deployment receipts. Build from the qualified
source checkout, never from the older native working tree.

Capture service IDs, image IDs, start/config hashes and credential/configuration
hashes after API promotion. Resolve the release override privately and compare
it with the running collector. Its only changes are the immutable collector image
and `REPLAY_CONTEXT_WRITES=true`; detail remains 48 hours and outer retention
seven days. Raw cadence, author retention, schema and maintenance settings retain
their recorded values.

The reviewed private release override is
`/home/onnwee/.local/state/headcount/context-release-20261109T205415Z/collector-release.override.json`.
The matching normal rollback override is `collector-rollback.override.json` in
that same directory. They resolve immutable image IDs; tags alone are insufficient.
The qualification record on the operational PR identifies the final source head,
image IDs, override hashes, baseline and completed evidence.

The activation command, after the checkpoint and baseline guards, is:

```sh
docker compose \
  --project-directory /home/onnwee/Work/Subcult/headcount \
  --env-file /home/onnwee/Work/Subcult/headcount/.env \
  -f /home/onnwee/Work/Subcult/headcount/compose.yaml \
  -f /home/onnwee/.local/state/fleet-repairs-20261109/headcount-collector.override.json \
  -f /home/onnwee/.local/state/headcount/wave3-20261109T165313Z/collector-release.override.json \
  -f /home/onnwee/.local/state/headcount/context-release-20261109T205415Z/collector-release.override.json \
  up -d --no-deps --no-build --pull never collector
```

Normal rollback uses the same command, replacing only the final override with
`/home/onnwee/.local/state/headcount/context-release-20261109T205415Z/collector-rollback.override.json`.
The rollback override retains 48-hour/seven-day retention and disables referenced
writes; the older emergency stop-pruning override is a different recovery action.

After approval recreate only `collector` with `--no-deps --no-build --pull never`.
Read back the image/mode immediately and compare API, database, backup, credentials
and native checkout/configuration hashes with the activation baseline.

Require live provider health, viewer/chat/follower/account-observation advancement,
new v11/peer-v2 scoring and version-2 private snapshot writes. Export and replay
both an old inline and a new referenced packet; compare digests and frozen stored
outputs. Check private-role denials, context reuse and bounded retention/GC progress
without logging packet contents, context values, author data or credentials.

## Measurement

Record per-new-snapshot/pass committed fragment bytes, newly inserted context
bytes, new/reused context counts and fallback-inline rows. Compare those counters
with actual compressed datum sizes for the same committed population. Context
reuse depends on stream timing and changing peer sets; report those changes.

Report live logical payload, allocated relation/TOAST/index bytes, dead/reusable
space estimates, archive size and restore behavior as separate quantities. Include
reference/index overhead when comparing allocated size. Do not attribute the first
retention sweep, new population growth or fixture-volume removal to deduplication.
No filesystem shrink or future savings percentage follows from a bounded cohort.
Keep inherited autovacuum settings; blocking rewrites/global tuning are outside
this release.

## Recovery matrix

| Component | Recovery behavior |
|---|---|
| Candidate collector with referenced writes disabled | Writes new inline rows; retained referenced evidence stays readable. |
| Retained tiered collector image from #24 | Writes inline rows under the additive schema and keeps the approved 48-hour/seven-day policy. Rehearse against mixed data before use. |
| Compatible research reader from #20 or newer | Hydrates inline/referenced storage into self-contained schema-1 packets. |
| Older research binary | Use verified hydrated exports, or the compatible reader release for database export. |
| Database recovery | Separate operator decision; a full restore loses observations after the recorded transaction recovery point. |

The normal collector rollback override sets `REPLAY_CONTEXT_WRITES=false`, selects
the retained tiered image and keeps both retention values unchanged. Recreate only
that service and repeat provider/ingestion/inline-write checks. Leave additive
schema, admitted migration hashes, API role and retained contexts in place.
Disabling referenced writes does not itself expire contexts or restore deleted
history. To stop pruning and GC in an emergency, the existing retention recovery
procedure sets outer retention to zero in a separately recorded scoped restart.

Record activation/live verification and any recovery in canonical Machinum Kvant
notes, with the private receipt root and hashes. Retain backups, protected exports
and rollback images; clean up only owned fixture containers, anonymous volumes and
networks after qualification.
