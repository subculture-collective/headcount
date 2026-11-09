# Additive replay-context admission

Migration `008_replay_contexts.sql` adds storage metadata to `score_snapshots`,
immutable context bytes and protected snapshot-field references. Issue #18 does
not enable referenced writes, hydrate readers, collect orphan contexts, rewrite
old snapshots or change retention. Existing score writes still insert complete
inline inputs with default storage version 1 and a null full-input digest.

## Schema and ownership

Use [ADR 001](../adr/001-immutable-replay-context.md) for canonical bytes, hash
domains and compatibility. Producers/hydration readers own typed encoding.
Store primitives verify raw bytes against their digest and envelope metadata,
compare existing bytes on conflict, and retain a key-share lock through binding
and transaction completion. The caller owns the transaction and must roll it
back on error. No Store update or replacement path exists. Database administrators
remain trusted; readers detect corrupted content.

Only fields omitted from version-2 fragments with a full-input digest can bind
a context. Unique keys prevent duplicate bindings; a composite foreign key
requires the field and context kind to agree. An indexed restricting foreign key
protects referenced content. Snapshot deletion cascades references, leaving shared
contexts intact. Transaction rollback removes uncommitted content/references.
No collector call site uses these primitives yet.

Both new tables revoke all privileges from PUBLIC and susq_api, overriding old
default SELECT grants. The image fixture explicitly checks denial. The predeploy
hook adds both private SELECT checks to existing write/security-definer checks.
Its optional-table lookup supports pre-migration current main; the exact migration
hash/ledger comparison continues to fail closed until native admission.

## Qualification and native checkpoint

1. Record the exact green PR head, packaged migration hashes, native ledger,
   environment-file hash, API/collector/database/backup identities, and current
   registration/profile hashes. Keep secrets, raw inputs and complete private
   registration files outside the repository.
2. Select a consistent retained backup with independently verified full restore
   counts/versions. Record its timestamp/hash and recovery-point limits. Rehearse
   the additive migration on restored disposable data using the exact candidate
   image's SQL. Verify unchanged old evidence, inline defaults, context reuse,
   protected bindings, transaction rollback and explicit API-role denial.
3. Export an old inline row with an old research binary after migration and
   verify the self-contained packet. Run the full suite and the candidate image's
   read-only API fixture. No collector or provider credentials are needed.
4. Review the concrete additive DDL and native registration update. Apply only
   in the native checkpoint, with a short lock timeout and a single transaction
   covering migration plus ledger insertion. A timeout/error rolls back both.
   Do not alter old migrations, bypass the ledger guard or have CI apply DDL.
5. Read back all nine exact names, zero new contexts/references, inline defaults,
   API-role denial and continued native ingestion. Install the reviewed hook at
   an immutable hash path and update registration/profile to its exact hash and
   the admitted migration set. Preserve prior files as private recovery receipts.
6. Merge only the qualified head. Require canonical-main API CI/admission to
   pass, then compare the public build marker/health to the accepted image.
   Confirm collector, database, backup and native configuration retain their
   recorded identities. Record sanitized qualification evidence on the PR and
   in the canonical operations report.

The API keeps serving during the additive checkpoint. Until the matching main
image and registration agree, promotion must fail closed; this is not permission
to relax migration checks to make an older queued deployment pass.

## Rollback and recovery

Leave additive schema in place for binary rollback. Old collectors keep writing
inline rows; old research binaries can still export those rows. Future referenced
writes require the compatible hydration reader from its separate issue.

Do not delete schema or revert the registered migration set merely to admit an
old API image. Prepare a reviewed rollback API artifact carrying the admitted
migrations, or keep the qualified API while rolling back only a collector. A
failed migration transaction needs no schema rollback. Restoring a backup is a
separate decision that loses writes after its recovery point; disabling a binary
feature cannot recreate deleted evidence. No data expiry belongs to this issue.
