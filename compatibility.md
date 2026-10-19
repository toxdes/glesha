# Compatibility policy

Glesha must remain able to recover data created by earlier supported versions.
Presentation improvements must not change persistent identities or data formats.

- Preserve binary OpenPGP archives and detect xz/gzip/bzip2 from the decrypted stream.
  Never require a current filename prefix to read an archive. Existing encb
  payloads stay unchanged; importing them does not recompress or replace them.
- Version manifests, catalogs, registry formats and JSON output explicitly.
  Introduce storage changes with documented, tested migrations that preserve the
  old database until the replacement is validated. Unknown versions fail early
  without modifying data. Do not infer schema compatibility from a filename.
- Preserve set/snapshot UUIDs, path-node identities, ancestry, source bindings,
  provider identities and exact object references. Never reuse deleted node IDs
  or reinterpret recorded keys, hashes and storage-class observations.
- New model fields need defined defaults for older records. Optional fields must
  not make earlier records unreadable. Required fields need a format version and
  migration or conversion path. Renaming Go fields must preserve serialized names.
- Keep existing command and flag spellings as aliases when adding new names.
  Removing or changing a supported option requires a documented deprecation path.
  Help and manual pages must reflect aliases and defaults.
- Keep versioned JSON stable for automation: byte counts remain integer bytes;
  colors, progress and human size formatting never enter JSON result output on stdout. Format
  changes that break parsers require a new output version.
- Retain fixtures for supported historical archives and database/output versions.
  Test both successful reads and rejection without mutation of unsupported data.

The original clean-slate rewrite deliberately uses new state rather than migrating
legacy application databases. That boundary remains explicit. The current
catalog format is schema version 2. Status reporting adds an optional
`uploaded_at` location field; older records default to an unknown upload time.
The advisory local metadata-size cache has its own version, defaults to unknown
when absent, and does not change catalog schemas, archives, keys or identities.
Status JSON keeps its version 2 envelope and existing fields, adding byte counts,
availability, timestamps and estimates. Human formatting does not enter JSON.

XZ is an additional archive format detected by its stream signature. It does not
change catalog or manifest schemas. Existing sets and pending runs retain their
recorded compression; only new defaults use xz. Old gzip/bzip2 payloads and encb
fixtures remain supported unchanged, including mixed-format incremental chains.
Metadata batches remain gzip. The xz decoder supports plain LZMA2 filter streams;
unsupported filters and excessive dictionaries fail without publishing output.

Pending run requests optionally record `auto_cleanup`. Missing fields in older
requests mean retain the archive. New runs enable cleanup only for automatically
named outputs; `--keep-archive` and explicit relocation disable it persistently.
Cleanup follows successful remote publication and local commit, without changing
archive bytes, snapshot identity or the serialized local file reference.

Chunked snapshots use archive manifest version 2 and `layout: chunked`, with an
ordered chunk list and a separately encrypted descriptor version 1. Single-file
manifest version 1 remains unchanged. Missing `layout`, `chunks`, set `chunked`
and `spool_max` fields retain the single-file behavior; unknown layouts are rejected.
Chunks contain tar fragments, so raw extraction of one chunk is not supported.
The current limit is 65,536 chunks per snapshot. Source bytes must match the
frozen inventory when resuming; no encryption or compression state is persisted.
AWS lifecycle observations cover each chunk independently. Command-driven moves
and storage-class changes currently reject chunked snapshots.

State management adds optional catalog metadata (`last_sync`, `baseline_changed`) and persists live
storage observations using existing location/chunk fields. Archive bytes and catalog
schema version 2 are unchanged. Status JSON remains version 2 and adds a condition,
class-byte breakdown and optional sync timestamp; old fields retain their types.
The advisory operation-result file is local only; unknown state values fail closed.

CLI adds status --format plain|table|csv|json, catalog sync/resolve and cleanup.
Existing catalog pull/push/reconcile/abandon spellings remain supported.
Import now publishes automatically only when a remote destination is configured;
local-only imports and their existing archives stay local and unchanged.
Cleanup recognizes only managed local copies with completed, published snapshots;
it never deletes catalogs, external archives or cloud objects.

Default local state now uses the platform application-data directory, separate
from configuration. Existing `catalog.directory` overrides retain their meaning.
Older post-clean-slate state under the config directory is detected without opening
or changing it; `state migrate` explicitly moves it to the new default or configured
destination. Only local snapshot payload paths are rebased. Source mappings, cloud
references, archive bytes, schema versions and identities are preserved. Original
databases are retained in `.state-migration-backup`; a version 1 migration record
supports rollback/retry after interruption. Unknown versions and conflicting
destinations fail closed. Migration does not scan or convert pre-clean-slate state.

New cold retrievals default to three days; explicit configuration and saved pending
request durations retain precedence. Restore jobs optionally retain a Retrievals
history and import jobs an optional CheckedAt timestamp; older jobs remain readable.
The local per-job retrieval cache is version 1. Unsupported cache versions fail
without replacement. Status JSON remains version 2 with an optional retrievals
array; existing fields retain their types. CSV appends a retrievals JSON column.
Human timestamps use local time; persistent timestamps and JSON retain UTC.

All commands accept --format=plain|json; status retains table/csv. The former
--json flag is removed by explicit user decision before wider adoption; use
--format=json instead. Existing version 2 result envelopes and
record fields remain unchanged. Empty listings remain empty. New command result
kinds cover local operations, no-change runs, cleanup and catalog operations.
Progress and diagnostics use a separate version 1 JSON Lines protocol on stderr;
progress never enters the result stream. Human progress can use plain lines when
stderr is redirected or TERM=dumb. No archive or database formats change.
