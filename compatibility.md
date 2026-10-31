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
stderr is redirected or TERM=dumb. This output protocol does not change archive
or database formats.

Portable state introduces publication/descriptor version 3 while retaining SQLite
catalog schema 2 and readers for version 2 metadata. New publications use version
3; an existing version 2 publication already in progress finishes with its recorded
version before migration. An unlocked legacy catalog migrates through a complete,
validated checkpoint at its existing authoritative revision location. Earlier
metadata and pre-replacement local databases remain available. Older executables
reject the version 3 revision before creating another archive; upgrade all writers.
This is a state migration, not an archive conversion. Set/snapshot IDs, ancestry,
payload bytes, hashes, exact object versions, compression and historical passwords
are preserved. Unknown state versions fail validation before publication.

The version 1 discovery directory contains only set UUIDs, authoritative remote
identities and metadata prefixes, plus predecessor records for authority changes.
Independent immutable records are replicated across configured Glesha buckets.
Names, source roots, and archive contents are not present in this directory.
Its records are locators; catalog contents must still pass revision, checksum,
decryption and identity validation. Conflicting authorities require explicit
selection and preserve both original histories. Missing access never means that
the set is empty. New local discovery tables are additive; unknown record versions
are rejected. Duplicate private names remain addressable by UUID, and ambiguous
name lookups fail explicitly.

Provider moves commit a retirement revision at the source before activating its
new authority locator. Stale writers cannot publish past that revision. Redirect
records retain locator ancestry so discovery remains recoverable after partial
registry replication. Reusing a retired destination requires an explicit move.
An unchanged source revision is required for retirement; concurrent source
changes stop the move with pending work preserved. A clean local catalog follows
the new authority only after verifying that its historical snapshots are retained.
Local unpublished changes require explicit resolution across an authority change.

Bare catalog sync and its --all alias discover configured buckets and cache
encrypted metadata without passphrase prompts or archive payload requests. A
downloaded-state index, discovery report and registry-repair journal each use
version 1. Interrupted or failed discovery remains visible; failed replica repair
does not require another archive upload. Targeted sync and commands requiring
catalog contents unlock the selected catalog. An incorrect passphrase never
removes a discovered set or replaces a usable database. Active source bindings
stay local; new descriptors omit their paths. Incoming catalogs preserve only
this computer's known archive/chunk cache paths. Raw archive objects lacking a
catalog still require explicit import/recovery. Existing import limitations and
per-snapshot passphrase overrides remain supported.

CLI spellings follow the output protocol above. Catalog sync intentionally uses
version 2 catalog_state records and a catalog_sync_summary instead of the earlier
synced, skipped and set_error records. Each catalog_state includes its set UUID,
state and optional error; the envelope version stays unchanged. list --remote
is deprecated but retained; list and status read local inventory without prompts.
Portable mutations synchronize first and publish committed state afterward;
standalone archive/file commands remain independent of remote state.

Restore now accepts existing directories without overwriting files or following
destination symlink ancestors. Verified staging and version 1 publication journals
allow interrupted publication to resume; already-published files must still be
the staging files, and created directory identities are checked before metadata
changes. Uncertain directory-creation ownership fails with preserved staging.
An interrupted publication stays pinned to its original snapshot. New archive
headers omit unfrozen source access/change timestamps so chunked retries produce
the same tar stream. Historical archives retain their existing decoding behavior.

Explicit `--from`/`--to` arguments now bound all remote access to their union,
including metadata discovery, migration and registry replication. Commands that
previously implicitly contacted other providers now require those providers to
be selected explicitly (for example, `move SET --from A --to B`). Without
either flag, configured and persisted remote choices retain their prior behavior.

Catalog sync retains version 2 JSON catalog_state fields and historical state
values. Additive result (updated/up_to_date) and locked fields separate this run's
outcome from catalog access. A final catalog_sync_summary record reports counts
and registry-copy repairs. Human output summarizes unchanged runs and reports
changed catalogs individually. Metadata progress no longer calls catalogs or
registry records archives. Routine cached checks remain silent.

Catalog sync groups shared failures in human output while preserving individual
JSON catalog_state errors. Discovery and registry failures use additive
catalog_sync_error records. A concise terminal error preserves underlying error
identities and exit codes without repeating the report. Color-enabled failures
and incomplete summaries are red; JSON and color-disabled output omit ANSI.

Known empty legacy registries no longer block populated default state. Only a
real directory containing no files, or a single supported registry database
with known empty tables, is ignored; unknown tables/files, pending state and
unsupported databases remain protected. Both populated locations require an
explicit `--state-dir` selection. Migration reports no work for absent/empty
legacy state without moving or deleting either location. `--state-dir` overrides
`catalog.directory` per invocation and can select a fresh migration destination.

Restore --cancel archives supported local restore job/publication records and
matching advisory restore results without deleting destination/staging files or
changing cloud state. Optional --into limits the selection. Unknown task versions
are rejected before any record is moved. Serialized records stay unchanged.
Interrupted interactive passphrase reads return context cancellation (exit 130)
and restore terminal modes. File-based passphrase handling is unchanged.

CLI presentation now separates stdout results from stderr activity and prompts.
Human lists have headers and explicit empty states; unreadable catalog totals
are marked as partial and unknown snapshot counts use a dash. Informational text
is neutral; state and diagnostic colors are semantic. Label controls are escaped.
Existing version 2 JSON records retain fields and types and never contain prose,
color or progress. The new catalog unlock command operates on cached metadata
only and emits an additive catalog_unlocked record; existing sync behavior stays
unchanged. Existing aliases and exit codes remain supported.
