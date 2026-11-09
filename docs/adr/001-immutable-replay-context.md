# Immutable replay reference context

Status: accepted design for issues #18, #20, #22, #23, and #25; no storage
migration or writer activation is included in this decision. Resolves #15.

## Why full replay inputs repeat

`score.Snapshot` freezes the evidence used by one scoring pass. Its `peers`,
`partners`, and optional `behavior_peers` arrays can repeat across passes.
`series`, account summaries, behavior, shared-account results, stream metadata,
and observation time remain specific to the snapshot.

`research.Digest` hashes `json.Marshal(score.Snapshot)`. Exported packets use
snapshot schema 1, and `Packet.Verify` rejects other schemas. Storage changes
must preserve those full typed inputs, their digest, and `score.Evaluate` results.
The mutable peer/behavior caches cannot supply historical replay evidence.

## Factor each nonempty reference array separately

Use three context kinds: `peers`, `partners`, and `behavior_peers`. Separate
objects let stable peers be reused when synchrony partners change. Keep empty
arrays and nulls inline; they do not justify a reference row. Do not factor
the growing per-stream series in this release.

For context schema 1, canonical bytes are Go `encoding/json.Marshal` applied
to this ordered envelope:

```json
{"context_schema":1,"kind":"partners","value":[{"Login":"fixture-two","OverlapMinutes":60,"Onsets":[1,3]}]}
```

The envelope fields are declared in that order. `value` uses the existing typed
slice: `[]store.PeerStat`, `[]detect.Partner`, or `[]store.BehaviorPeer`.
Preserve array order, struct field order, `omitempty`, null versus empty arrays,
Go's JSON string escaping, and its finite-number representation. Do not sort
peers or onset arrays, round numbers, or apply a different JSON canonicalizer.
Map keys inside typed values retain Go's deterministic JSON ordering.

The digest is lowercase hexadecimal SHA-256 over the ASCII bytes
`headcount.replay-context.v1`, one zero byte, then the canonical envelope bytes.
The domain, kind, and context schema prevent accidental reuse across formats.
Future incompatible encodings require a new context schema/domain and reader.

The [synthetic vectors](../fixtures/replay-context-v1.json) fix these bytes and
digests, including legacy omitted peer metadata, reordered arrays, and nulls.
`TestReplayContextContractVectors` checks them against the current Go types.

## Keep storage versions separate from packet schema

The additive storage design is:

- `replay_contexts`: digest primary key, context kind/schema, canonical envelope
  bytes, and creation time. Store canonical bytes once as TOAST-compressible
  `bytea`; do not duplicate the same payload in JSONB.
- `score_snapshots`: a storage-version discriminator defaulting to 1, plus a
  nullable full-input digest. Version 1 means today's complete inline input.
  Version 2 requires the original full typed-input digest.
- `score_snapshot_contexts`: one reference per snapshot identity and factored
  field. The snapshot foreign key cascades on deletion. The context foreign key
  restricts deletion. Unique keys prevent duplicate bindings for a field.

Version-2 `input` omits only the referenced fields. It is an internal stored
fragment, never a packet or an input to `Evaluate`. `Snapshot.Schema` stays 1.
All new tables are private: explicitly revoke access from `susq_api`, including
any default grants. Extend the API admission checks for the new private tables.

## Hydrate before export, checksum verification, or evaluation

One snapshot-loading boundary reads the fragment, references, and contexts in a
consistent database transaction. It verifies each digest over the stored bytes,
checks the supported kind/schema and field binding, and restores the arrays.
It then decodes the complete `score.Snapshot` and verifies its full-input digest.

Reject unsupported storage versions, unknown kinds, missing contexts, digest
mismatches, duplicate references, or a field that is both inline and referenced.
Never substitute a current cache value or silently treat missing evidence as nil.
Keep `store` independent of `score`: storage primitives return raw records and
verified context bytes; typed encoding/verification belongs above that boundary.

Research export emits the existing self-contained `Packet`. Offline replay,
review, sampling, calibration, and persistence need no database or context server.
Legacy inline snapshots use their existing typed digest path without rewriting.
The actual export loader in `cmd/research/main.go` must adopt hydration before
any writer can emit version 2.

## Write and expire evidence atomically

Within the existing score transaction, insert each context if absent, compare
canonical bytes on conflict, and hold a key-share lock until references commit.
An existing digest with different content is an error, never an upsert overwrite.
Write the fragment, full-input digest, references, and score together. Retrying
an existing snapshot identity compares complete hydrated input and output.

Garbage collection selects a bounded set of unreferenced contexts with update
locks and `SKIP LOCKED`, then rechecks references in a fresh statement. Foreign
keys remain the final guard against racing inserts. Cancelled or failed batches
roll back; deletion of one snapshot cannot remove another snapshot's context.
Context content has no update path. Recreating identical content after expiry
uses the same digest. Database administrators remain trusted operators.

## Compatibility and delivery order

| State | Supported behavior |
|---|---|
| Old inline row, new reader | Existing digest and replay result stay valid. |
| Referenced row, new reader | Hydrate, verify full digest, export schema-1 packet. |
| Exported packet after database expiry | Offline verification/replay still work. |
| Old research CLI on referenced database | Unsupported: use the new export reader. |
| Old collector after additive migration | May keep writing inline rows; references on deleted rows cascade. |
| Old collector while context GC is stopped | Orphan context bytes may remain; retained replays remain valid. |

Admit additive private schema and readers first. Keep referenced writes disabled
until the tiered-retention release and the mixed-format restore/export rehearsal
pass. Update native migration/hash registration before API promotion. Enable
referenced writes only in the collector release. Leave old inline rows to normal
retention; a bulk rewrite is outside this plan. A binary rollback cannot recreate
expired evidence and does not make old research tools context-aware.

Required implementation checks include hash collisions, omitted legacy
`MetricVersion`, nil/empty preservation, map-key ordering, changed context,
reference/write/GC races, missing or corrupt objects, migration replay, private
role denial, and mixed-format backup/restore. The contract tests demonstrate
lossless field decomposition, not a production storage implementation.

## Measurement limits

The November 9 investigation found substantial repeated peer context, but
changing partner windows may prevent partner reuse. Measure both separately.
The bounded compression study described in the PR uses actual input samples in
an isolated PostgreSQL database, the same LZ4 setting for both variants, and a
per-reference overhead budget. It is a feasibility estimate, not a forecast for
the entire database or a benchmark of the future canonical-bytea codec.

A bounded mixed-version sample used six recent snapshots for each of two
streams per scoring version (9 and 11), 24 inputs total. Re-encoding both
variants in isolated PostgreSQL 17.11 with LZ4 produced:

| Measure | Bytes or count |
|---|---:|
| Inline JSONB payload | 5,229,917 bytes |
| Sparse inputs plus distinct JSONB context payloads | 2,942,539 bytes |
| Conservative reference overhead allowance | 18,432 bytes |
| Distinct contexts | 33 |
| Payload reduction including the allowance | 43.4% |

The allowance is 256 bytes for each of 72 possible references. It is not an
index-size measurement. Original production stored bytes are not a fair baseline
for this comparison because the two representations need the same encoding and
compression setting. Raw samples stayed private in the disposable database.

The writer issue must measure distinct contexts, actual new bytes, reference
overhead, and reuse on representative workloads before claiming production
savings. No detector, score version, packet schema, or sampling probability
changes as part of this design.
