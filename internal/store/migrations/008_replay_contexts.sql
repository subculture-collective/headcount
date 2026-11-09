-- Additive storage only. Existing writers keep complete inline schema-1 input.
ALTER TABLE score_snapshots ADD COLUMN storage_version smallint NOT NULL DEFAULT 1
 CHECK (storage_version IN (1,2));
ALTER TABLE score_snapshots ADD COLUMN input_digest text
 CHECK (input_digest ~ '^[0-9a-f]{64}$');
ALTER TABLE score_snapshots ADD CONSTRAINT score_snapshots_referenced_digest
 CHECK (storage_version=1 OR input_digest IS NOT NULL);

CREATE TABLE replay_contexts (
 digest text PRIMARY KEY CHECK (digest ~ '^[0-9a-f]{64}$'),
 kind text NOT NULL CHECK (kind IN ('peers','behavior_peers')),
 context_schema smallint NOT NULL CHECK (context_schema=1),
 canonical bytea NOT NULL CHECK (octet_length(canonical)>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(digest,kind)
);
ALTER TABLE replay_contexts ALTER COLUMN canonical SET COMPRESSION lz4;
CREATE TABLE score_snapshot_contexts (
 stream_id bigint NOT NULL,
 observed_at timestamptz NOT NULL,
 version int NOT NULL,
 field text NOT NULL CHECK (field IN ('peers','behavior_peers')),
 context_digest text NOT NULL,
 PRIMARY KEY(stream_id,observed_at,version,field),
 FOREIGN KEY(stream_id,observed_at,version)
 REFERENCES score_snapshots(stream_id,observed_at,version) ON DELETE CASCADE,
 FOREIGN KEY(context_digest,field) REFERENCES replay_contexts(digest,kind) ON DELETE RESTRICT
);
CREATE INDEX score_snapshot_contexts_digest_idx ON score_snapshot_contexts(context_digest);
-- The old default grants include future public tables. Revoke them explicitly.
REVOKE ALL ON replay_contexts,score_snapshot_contexts FROM PUBLIC,susq_api;
