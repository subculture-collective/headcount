# Replay and TOAST maintenance

This runbook scopes routine maintenance to `public.score_snapshots` in the
native Headcount database (`headcount-db-1`, database/owner `susq`). Issue #21
adds diagnostics and recommendations. Applying production settings belongs to
issue #24 after its backup/export and deletion checkpoint.

## Read-only inventory

Run `retention-preview` with a fixed as-of time and a new private output file as
documented in [the research workflow](../research-workflow.md). Its storage
section includes main/TOAST table and index bytes, live/dead estimates, manual
and automatic vacuum/analyze timestamps and vacuum counters, per-relation
options, global defaults, prepared-transaction count, and aggregate transaction
age/xmin indicators. It never prints packets, author IDs, SQL text or credentials.

Empty relation options mean inherited defaults. Inspect the TOAST relation's own
options as well as its parent's. PostgreSQL statistics can lag or reset: null
vacuum timestamps are not evidence that vacuum never ran, and estimated dead
rows are not a byte count. The database-level statistics reset time does not
identify every possible per-table counter reset. Activity visibility depends on
the connection role; incomplete visibility cannot rule out blocking transactions.

Read effective defaults and overrides with the existing local database access:

```sh
docker exec -i headcount-db-1 psql -X -v ON_ERROR_STOP=1 -U susq -d susq <<'SQL'
BEGIN READ ONLY;
SELECT c.oid::regclass AS relation, c.reloptions
FROM pg_class c
WHERE c.oid='public.score_snapshots'::regclass
   OR c.oid=(SELECT reltoastrelid FROM pg_class WHERE oid='public.score_snapshots'::regclass);
SELECT name,setting,unit FROM pg_settings
WHERE name IN ('autovacuum','track_counts','autovacuum_naptime',
 'autovacuum_vacuum_threshold','autovacuum_vacuum_scale_factor',
 'autovacuum_vacuum_cost_limit','autovacuum_vacuum_cost_delay');
SELECT count(*) FILTER(WHERE backend_xmin IS NOT NULL) AS transactions_with_xmin,
 max(clock_timestamp()-xact_start) AS oldest_transaction
FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid();
SELECT count(*) AS prepared_transactions FROM pg_prepared_xacts
WHERE database=current_database();
SELECT phase,heap_blks_total,heap_blks_scanned,heap_blks_vacuumed
FROM pg_stat_progress_vacuum WHERE relid='public.score_snapshots'::regclass;
ROLLBACK;
SQL
```

Check long transactions, replication-slot xmin/catalog_xmin, and prepared
transactions before interpreting poor reclamation. Inspect aggregate ages and
ownership through the existing privileged operations session; do not log query
text or terminate another session automatically. End only an owned test
transaction. Do not disable global autovacuum or `track_counts`.

## Disposable churn measurement, November 9, 2026

PostgreSQL 17.11 in an isolated matching TimescaleDB container compared two
identical replay-shaped tables, each with 2,000 rows containing 16,384 characters
of incompressible synthetic JSON payload. Each payload concatenated 512 MD5
values derived from row/element numbers. A primary key and JSONB input/output
columns reproduced heap/index/TOAST churn; no production packets were copied.
After an initial `VACUUM (ANALYZE)`, delete the oldest 200 rows, then measure
`pg_stat_all_tables` on the parent and its TOAST relation and
`pg_total_relation_size` before/after automatic and routine vacuum.

A read-only native metadata check confirmed global autovacuum enabled, a
10-second launcher interval, threshold 50, scale factor 0.2, and no replay/TOAST
overrides. The baseline inherited that vacuum threshold and scale factor. The treatment
set threshold 100 and scale factor 0.02 on both parent and TOAST. Global
maintenance remained enabled. The fixture alone accelerated its launcher interval
from 10 seconds to one second; it was reset afterward. Production cadence and
workload were not changed. The first observed treatment vacuum completed within
a 1.07-second observation interval, not a production latency prediction.

| Stage, per table | Baseline dead heap / TOAST estimates | Treatment dead heap / TOAST estimates | Allocation, both tables individually |
|---|---:|---:|---:|
| Initial 2,000 rows | 0 / 0 | 0 / 0 | 33,431,552 bytes |
| After 200 deletes | 200 / 1,639 | 200 / 1,639 | 33,431,552 bytes |
| After treatment autovacuum | 200 / 1,639 | 0 / 0 | 33,431,552 bytes |
| After routine vacuum on both | 0 / 0 | 0 / 0 | 33,431,552 bytes |
| After 200 replacement inserts | 0 / 0 | 0 / 0 | 33,554,432 bytes |

Treatment recorded one automatic heap vacuum and one TOAST vacuum; the baseline
recorded none at that observation. Routine `VACUUM (ANALYZE, TRUNCATE FALSE)`
removed the baseline's dead estimates too. Replacement payloads reused most
space: allocation grew 122,880 bytes while accepting another 200 large inputs.
This demonstrates reuse, not an exact free-space measurement. No `pgstattuple`
extension was installed and no filesystem-shrink claim is made.

The treatment triggers earlier on this workload: the initial heap dead-tuple
threshold is 140 rather than 450. TOAST has its own tuple population and trigger.
The synthetic scale and accelerated launcher do not establish production I/O
capacity, best cost limits, or a universal threshold.

## Proposed replay-only settings

For the later rollout, evaluate threshold 100 and scale factor 0.02 for replay
heap and TOAST, leaving global and cost settings unchanged. Capture existing
relation options first, then check post-prune dead-row growth, vacuum duration,
I/O latency and scoring/ingestion health. Apply only after review of that
production evidence. PostgreSQL's [autovacuum settings](https://www.postgresql.org/docs/17/runtime-config-autovacuum.html)
and [storage parameters](https://www.postgresql.org/docs/17/sql-createtable.html)
define the inherited defaults and table overrides.

The candidate SQL for the separate rollout is:

```sql
SET lock_timeout='2s';
SET statement_timeout='10s';
ALTER TABLE public.score_snapshots SET (
 autovacuum_vacuum_threshold=100, autovacuum_vacuum_scale_factor=0.02,
 toast.autovacuum_vacuum_threshold=100, toast.autovacuum_vacuum_scale_factor=0.02
);
```

Run it only against the reviewed native Headcount database. Read back both
relations' options using the inventory above. If these four options were absent
before activation, rollback is:

```sql
ALTER TABLE public.score_snapshots RESET (
 autovacuum_vacuum_threshold, autovacuum_vacuum_scale_factor,
 toast.autovacuum_vacuum_threshold, toast.autovacuum_vacuum_scale_factor
);
```

If options already existed, restore their recorded values instead. Reverting
settings affects future maintenance; it does not restore deleted evidence.

## Bounded routine maintenance

Use a separate scoped operations connection, outside a transaction. Set a short
lock timeout and a 60-second work budget. Under the later maintenance approval,
run these statements independently against `susq` in `headcount-db-1`:

```sql
SET lock_timeout='2s';
SET statement_timeout='60s';
SET vacuum_cost_delay='5ms';
SET vacuum_cost_limit=200;
VACUUM (TRUNCATE FALSE) public.score_snapshots;
ANALYZE public.score_snapshots (stream_id,observed_at,version);
```

A timeout may leave only part of a vacuum pass complete; inspect progress and
statistics before scheduling another bounded attempt. Analyze identity columns
without sampling large JSON values. Ordinary vacuum permits normal reads and
writes but still acquires locks; `TRUNCATE FALSE` avoids its optional tail-page
truncation lock. Confirm recent vacuum timestamps, dead estimates, allocation
and continued ingestion/scoring after maintenance. See PostgreSQL's
[routine vacuum guidance](https://www.postgresql.org/docs/17/routine-vacuuming.html).

`VACUUM FULL`, `pg_repack`, partition conversion and every blocking rewrite are
separate maintenance decisions. Measure required free disk, WAL/backup impact,
lock duration, replica/consumer constraints and a tested recovery path first.
They are never invoked by preview, retention, this runbook's routine commands,
or an API deployment. Ordinary vacuum generally makes space reusable; it does
not guarantee smaller files or a particular database-size reduction.
