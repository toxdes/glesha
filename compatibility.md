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
  colors, progress and human size formatting never enter JSON output. Format
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
