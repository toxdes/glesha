# glesha CLI reference

Create a standalone encrypted archive, or register a named set to keep a history
of cloud backups. The executable performs compression, encryption and transfers
internally. Full archives also work with ordinary GPG and tar.

Run `glesha help` for a command summary or `glesha help run` for complete options
and examples. `glesha run --help` shows the same command help. Installed manual
pages are named `glesha(1)` and `glesha-run(1)`, with one page per command.
The source pages are in [man/](man/); release packaging will be handled separately.

## Back up and recover a directory

```console
glesha create docs ~/Documents --to b2
glesha run docs --env /secure/cloud.env
glesha run docs --incremental --env /secure/cloud.env
glesha history docs
glesha browse docs --path Documents --recursive
glesha restore docs --into ~/recovered --env /secure/cloud.env
```

`create` only saves settings locally. `run` creates a full backup by default;
`--incremental` must be explicit. Give destinations at creation or run time:
there is no default provider. A set has a stable UUID and a local name;
each snapshot has its own UUID. Bound source directories cannot be replaced.
An incremental set keeps its complete history on one provider. `move` copies
and verifies the entire history before switching, retaining original copies.

## Output and progress

Human command output uses ASCII. Unicode names are shown with escapes without
changing stored names or JSON values. Help has wrapped descriptions, meaningful
argument names and separate global options.

`--color=auto` is the default. Use `--color=yes` to force color or `--color=no`
to disable it. Existing `always` and `never` spellings remain supported.
`--log-level=debug` enables operation and worker-budget diagnostics. Normal output
shows readable sizes such as `14.20 MiB`; JSON retains integer bytes and its versioned
structure.

Interactive stderr marks steps with `[+]` and shows a progress bar, processed bytes and average speed with two decimal places.
Scanning uses an ASCII spinner (`-`, `\`, `|`, `/`). Archiving shows processed/total
uncompressed file bytes and a percentage, counting hard-linked content once.
Incrementals count only included content. Finalization and metadata publication
show status without an indefinite bar. Upload counts include in-flight payload bytes; SDK checksum reads and retries
do not count the same bytes twice. A 100% transfer still waits for verification
and catalog publication. One renderer refreshes every 100 ms and uses about 80% of terminal width;
worker counters perform no terminal I/O. Progress is disabled when stderr is
redirected, for `--json`, and at `warn`, `error` or `silent` logging levels.
Color-disabled progress uses no ANSI escapes.

Catalog publication groups batch, descriptor and commit uploads into one step.
The archive upload remains a separate step; internal metadata checks are logged
at debug level.

XZ uses LZMA2 and CRC64 internally. Levels 1..9 choose dictionaries from 64 KiB
to 16 MiB, doubling per level; level 6 uses 2 MiB. These differ from external
`xz` presets. Higher levels may require `--memory-max` to be increased. Imports
reject unsupported filter chains and dictionaries beyond the decoder budget.
Existing sets retain their stored compression; `configure SET --compression xz`
changes future runs. Metadata batches keep their existing gzip format.

Missing human-readable values use `-`; JSON keeps its existing types. Routine
output contains results rather than explanatory footers. Estimation assumptions
and compatibility details are documented here and in the manpages.

Use `--jobs N` or `-j N` as aliases for `--transfer-workers N`.
Transfer workers bound multipart upload work across providers, not all HTTP connections. Downloads currently stream one object at a time. `--hash-workers` controls file hashing separately; tar and compression remain a streaming pipeline. More workers can increase memory use and do not always improve throughput.

## Selection, defaults and safe behavior

- `--to aws,b2`, `--to b2,aws`, and repeated `--to` flags are supported. Uploads run
  concurrently. Duplicates are removed while preserving order.
- `--from b2,aws` and repeated `--from` flags preserve fallback order. A readable
  registered copy takes precedence over charged cold retrieval. Standalone
  downloads need an explicit source; snapshot restores use the saved selection.
- Compression defaults to xz level 6. CLI settings override TOML and built-in
  defaults. Gzip is selectable. Reading detects compression from the stream.
- Output paths must be new. Incremental removed paths and type replacements ask
  `[y/N]`; use `--assume-yes` for automation. Historical snapshots remain available.
- Replacing a raw remote object requires `upload --force`. Move keeps source copies.
  Catalog pull preserves the old local catalog. Abandon preserves pending work.
- Repeat `run` or use `retry` after interruption. Completed archives and recorded
  uploads are reused. Unfinished scans, downloads or multipart parts may restart.
- Cold retrieval needs separate consent or `--request-retrieval`. Bulk is the
  lowest-cost default; `standard` and `expedited` are opt-ins. Deep Archive supports
  Bulk and Standard, typically up to 48 and 12 hours respectively. AWS expires the
  temporary readable copy automatically, after seven days by default.

## Credentials and passwords

Nonsecret defaults live in `glesha/config.toml` under the platform user-config
directory. Credentials come from the process environment or an explicit `--env`
file; existing variables take precedence. No sensitive file is rewritten.
Archive passwords use a hidden prompt or `--passphrase-file FILE` (`-` for stdin).
The catalog secret is separate: `GLESHA_CATALOG_PASSPHRASE`, a hidden prompt, or
`--catalog-passphrase-file FILE`. Passwords are never stored. For mixed-password
chains use repeated `--archive-passphrase-file SNAPSHOT_ID=FILE` overrides.

## Catalogs and compatibility

`registry.db` maps set names/UUIDs and stable remote identities. Each set UUID has
a `catalog.db` using SQLite 3. Parent/name nodes, binary hashes, typed versions
and snapshot membership intervals preserve history without duplicating unchanged
inventories. Paths and totals are derived locally. Deleted node IDs are not reused.
All user values use SQL parameters. Browsing reads only the local catalog.

Remote metadata uses encrypted descriptors, compressed change batches and periodic
checkpoints. Deltas contain changed tree, snapshot and location records. AWS
publishes a conditional commit only after its batch and descriptor are complete;
abandoned proposals do not consume revision numbers. B2 detects and preserves
conflicting siblings. Concurrent remote writers are unsupported.

Existing encb archives remain unchanged: import downloads them once for cataloging
and uploads no replacement payload. Unknown dates remain unknown unless supplied.
Source mappings are optional for browsing/restoring imports and required for runs.
`--baseline new` explicitly makes an import current; `configure --baseline ID`
selects an existing snapshot.

Archive, catalog and JSON versions are explicit. This presentation update changes
no database schema, persistent model field, object key or archive format. Future
storage changes require an explicit migration path; unsupported versions fail
without altering files. CLI aliases remain supported when new spellings are added.
JSON stays suitable for scripts even when human formatting evolves. Old catalogs
are not silently migrated or deleted. See [compatibility.md](compatibility.md).

## Commands and every supported option

Flags can appear before or after positional arguments. Put operation-specific
flags after the command. `--` ends option parsing for paths beginning with `-`.
The global options below are also included in each command's option list.

### create

```text
glesha create SET PATH... [options]
```

Register a backup set and its source paths locally. This creates no archive and makes no cloud requests.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--catalog-to STRING` | catalog remote or local |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression STRING` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--incremental-storage-class STRING` | AWS incremental storage class |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--prefix STRING` | glesha or encb |
| `--storage-class STRING` | AWS storage class |
| `--to VALUE` | remote names; comma-separated or repeated |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha create docs ~/Documents --to b2
glesha create photos ~/Pictures --to aws,b2 --compression gzip
```

### configure

```text
glesha configure SET [options]
```

Change a set's saved settings or map imported roots. Previously bound source paths cannot be replaced.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--baseline STRING` | explicit snapshot baseline |
| `--catalog-to STRING` | catalog remote or local |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression STRING` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--incremental-storage-class STRING` | AWS incremental storage class |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--prefix STRING` | glesha or encb |
| `--root VALUE` | archive root NAME=source PATH |
| `--storage-class STRING` | AWS storage class |
| `--to VALUE` | remote names; comma-separated or repeated |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha configure docs --compression gzip
glesha configure imported --root Documents=~/Documents --to b2 --catalog-to b2
```

### run

```text
glesha run SET [--full|--incremental] [options]
```

Create and upload a full backup, or an explicit incremental. Repeating a pending run resumes recorded work. Automatically named local archives are
removed after all uploads and catalog publication succeed. Use `--keep-archive` to
retain them. Explicit `--output` files are retained; failures keep archives for retry.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--archive-mode STRING` | auto, memory or stream |
| `--assume-yes` | approve user-data removal confirmations |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression STRING` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--full` | explicit full snapshot |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--incremental` | require a baseline on one remote |
| `--json` | versioned JSON output |
| `--keep-archive` | retain the local archive after a successful backup |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--output STRING` | new output file |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--prefix STRING` | glesha or encb |
| `--storage-class STRING` | AWS storage class |
| `--to VALUE` | remote names; comma-separated or repeated |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha run docs
glesha run docs --incremental
glesha run docs --keep-archive
glesha run docs --incremental --assume-yes --passphrase-file /secure/archive-password --catalog-passphrase-file /secure/catalog-password --env /secure/cloud.env
```

### retry

```text
glesha retry SET [options]
```

Resume a pending backup, move, catalog publication or storage-class change. Completed uploads are reused. The original cleanup policy is preserved;
`--keep-archive` retains the file after success. Explicitly relocated archives stay local.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--archive STRING` | relocated completed archive |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--keep-archive` | retain the local archive after a successful backup |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha retry docs
glesha retry docs --archive /new/location/backup.tar.xz.gpg
```

### list

```text
glesha list [--remote --from REMOTE...] [options]
```

Show known sets. Remote discovery registers published set descriptions without downloading backup payloads.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--from VALUE` | ordered source remotes; comma-separated or repeated |
| `--h` | show help |
| `--help` | show help |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--remote` | discover remote set descriptors |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha list
glesha list --remote --from b2 --env /secure/cloud.env
```

### status

```text
glesha status [SET] [options]
```

Show an ASCII table of catalog availability, committed snapshots, stored archive copies, remote metadata size, last upload and estimated annual storage cost in USD. `status SET` adds sources, snapshot identity, local database size, provider copies and separate archive/metadata estimates.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--from VALUE` | ordered source remotes; comma-separated or repeated |
| `--h` | show help |
| `--help` | show help |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--refresh` | check live metadata without requesting retrieval |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha status
glesha status docs --refresh --env /secure/cloud.env
```

Plain status uses local records and cached measurements, without cloud requests.
A local catalog is `missing`, `local`, or `cached` (previously synchronized or
published). Cached does not promise that the remote has no newer revisions.
`--refresh` checks recorded archive metadata and lists current metadata objects;
it does not download or pull catalogs or request cold restoration.

Local SQLite bytes and compressed/encrypted remote metadata bytes are distinct.
New publications record their metadata sizes; older records remain unknown until
refreshed. Remote measurements count current visible objects, including discovery
records, and exclude hidden or older versions. A publication after a listing may
invalidate full coverage until the next refresh, rather than double-counting
pending proposals.

The last upload includes recorded payload uploads and catalog publication writes.
Older records without upload timestamps show `-` in human output. Import dates are not used
as upload dates. A remote metadata measurement may provide its latest write date.

USD/year estimates include recorded archive copies across providers plus known
remote metadata. They assume unchanged storage for twelve months, using the
[embedded pricing table](pricing/README.md), dated 2026-10-01. Unknown sizes or
rates mark the estimate partial (`*`). Estimates exclude API requests, retrievals,
egress, temporary restored copies, taxes, discounts, free tiers, pending/untracked
objects and untracked object versions. They do not predict future backup growth.
Intelligent-Tiering assumes Frequent Access unless archive access is observed.
JSON retains its version 2 envelope and original fields; additional byte counts
remain integers and USD estimates are numeric.

### history

```text
glesha history SET [options]
```

Show completed snapshots, newest first, including transfer and storage status.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--refresh` | check live metadata without requesting retrieval |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha history docs
glesha history docs --refresh --json
```

### browse

```text
glesha browse SET [options]
```

Browse a snapshot's file metadata in the local catalog. This makes no cloud requests and downloads no file content.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--path STRING` | catalog path |
| `--recursive` | include descendants |
| `--snapshot STRING` | snapshot ID or latest (default "latest") |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha browse docs
glesha browse docs --path Documents --recursive
glesha browse docs --snapshot SNAPSHOT_ID --recursive --json
```

### restore

```text
glesha restore SET --into DIR [options]
```

Reconstruct a full snapshot in a new destination. Cold retrieval requires separate, explicit consent.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--archive-passphrase-file VALUE` | snapshot ID=passphrase file |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--days INT` | readable-copy availability days |
| `--env STRING` | explicit credential environment file |
| `--from VALUE` | ordered source remotes; comma-separated or repeated |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--into STRING` | new destination directory |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--request-retrieval` | explicitly authorize retrieval charges |
| `--retrieval STRING` | bulk, standard or expedited (default "bulk") |
| `--snapshot STRING` | snapshot ID or latest (default "latest") |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha restore docs --into ~/recovered
glesha restore docs --snapshot SNAPSHOT_ID --into ~/recovered --from b2,aws
glesha restore docs --into ~/recovered --request-retrieval --retrieval bulk
```

### import

```text
glesha import SET (--file FILE | --from REMOTE --key KEY) [options]
```

Catalog an existing full archive without changing its encrypted bytes or uploading a replacement payload.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--baseline STRING` | explicit snapshot baseline |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--date STRING` | known backup date in RFC3339 |
| `--days INT` | readable-copy availability days |
| `--deep-verify` | verify associated object bytes |
| `--env STRING` | explicit credential environment file |
| `--file STRING` | local archive |
| `--from VALUE` | ordered source remotes; comma-separated or repeated |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--json` | versioned JSON output |
| `--key STRING` | exact remote object key |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--request-retrieval` | explicitly authorize retrieval charges |
| `--retrieval STRING` | bulk, standard or expedited (default "bulk") |
| `--root VALUE` | archive root NAME=source PATH |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha import old-docs --file encb-old.tar.gz.gpg
glesha import old-docs --from b2 --key backups/encb-old.tar.gz.gpg
glesha import old-docs --file encb-old.tar.gz.gpg --from b2 --key backups/encb-old.tar.gz.gpg --deep-verify
```

### move

```text
glesha move SET --to REMOTE [options]
```

Copy and verify the complete set history before switching providers. Existing source copies are retained.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--assume-yes` | approve user-data removal confirmations |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--days INT` | readable-copy availability days |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--json` | versioned JSON output |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--request-retrieval` | explicitly authorize retrieval charges |
| `--retrieval STRING` | bulk, standard or expedited (default "bulk") |
| `--to VALUE` | remote names; comma-separated or repeated |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha move docs --to b2
glesha move docs --to other-remote --request-retrieval --retrieval bulk
```

### archive

```text
glesha archive PATH... [options]
```

Create a standalone encrypted tar archive. No set name or cloud provider is needed.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--archive-mode STRING` | auto, memory or stream |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression STRING` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--output STRING` | new output file |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--prefix STRING` | glesha or encb |
| `--storage-class STRING` | AWS storage class |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha archive ~/Documents
glesha archive ~/Documents ~/Pictures --compression gzip --output personal.tar.gz.gpg
glesha archive ~/Documents --archive-mode stream --memory-max 128MiB --passphrase-file /secure/archive-password
```

### decrypt

```text
glesha decrypt FILE [options]
```

Validate an encrypted archive and write its compressed tar to a new output file.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--output STRING` | new output file |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha decrypt personal.tar.xz.gpg
glesha decrypt encb-old.tar.gz.gpg --output recovered.tar.gz --passphrase-file /secure/archive-password
```

### extract

```text
glesha extract FILE --into DIR [options]
```

Validate and extract an encrypted or plaintext compressed tar into a new directory.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--into STRING` | new destination directory |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha extract personal.tar.xz.gpg --into ~/recovered
glesha extract recovered.tar.gz --into ~/recovered
```

### upload

```text
glesha upload FILE --to REMOTE... [options]
```

Upload an existing file. Replacing an existing object key requires --force.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--force` | explicitly replace an existing remote key |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--key STRING` | remote key (default filename) |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--storage-class STRING` | AWS storage class |
| `--to VALUE` | remote names; comma-separated or repeated |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha upload personal.tar.xz.gpg --to b2
glesha upload personal.tar.xz.gpg --to aws,b2 --key backups/personal.tar.xz.gpg
```

### download

```text
glesha download KEY --from REMOTE... [options]
```

Download encrypted bytes from explicitly selected providers, trying them in the supplied order.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--from VALUE` | ordered source remotes; comma-separated or repeated |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--output STRING` | new output file |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha download backups/personal.tar.xz.gpg --from b2,aws
glesha download backups/personal.tar.xz.gpg --from b2 --output personal.tar.xz.gpg
```

### catalog

```text
glesha catalog pull|push|reconcile|abandon SET [options]
```

Synchronize metadata explicitly. Pull preserves the old local catalog; reconcile preserves conflicting histories; abandon preserves pending data.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha catalog pull docs
glesha catalog push old-docs
glesha catalog reconcile docs
glesha catalog abandon docs
```

### storage-class

```text
glesha storage-class SET --snapshot ID --class CLASS [options]
```

Change an AWS object's storage class with server-side copy. Archive identity and older versions are retained.

| Option | Meaning |
| --- | --- |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--class STRING` | AWS storage class |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--help` | show help |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--snapshot STRING` | snapshot ID or latest (default "latest") |
| `--storage-class STRING` | AWS storage class |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha storage-class docs --snapshot SNAPSHOT_ID --class DEEP_ARCHIVE
```

### check

```text
glesha check --to REMOTE... [options]
```

Test every selected provider using a small owned temporary object. Print separate
safety, write, metadata, verified read and cleanup results, followed by a summary.
`OK` means verified, `FAILED` includes the error, and `SKIPPED` makes no access
claim. A failed provider does not stop the remaining checks.

Buckets with default Object Lock retention are not uploaded to during checks.
If cleanup fails, the remaining key/version is printed; existing objects are never
replaced or deleted. `status` continues to describe backups rather than creating
test objects.

| Option | Meaning |
| --- | --- |
| `--json` | Versioned per-provider results and final summary. |
| `--L STRING` | debug, info, warn, error or silent (default "info") |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config STRING` | TOML configuration file |
| `--env STRING` | explicit credential environment file |
| `--h` | show help |
| `--hash-workers INT` | hash workers |
| `--help` | show help |
| `--log-level STRING` | debug, info, warn, error or silent (default "info") |
| `--memory-max STRING` | managed memory budget |
| `--to VALUE` | remote names; comma-separated or repeated |
| `--transfer-workers INT` | transfer workers |
| `-j N`, `--jobs N` | alias for `--transfer-workers`; memory budget may reduce the limit |
| `--version`, `-version`, `-v` | Show version and build revision. |

Examples:

```console
glesha check --to b2 --env /secure/cloud.env
glesha check --to aws,b2 --env /secure/cloud.env
glesha check --to b2 --json --env /secure/cloud.env
```

### help

```console
glesha help
glesha help run
glesha help catalog pull
```

Shows help without reading configuration or credentials. Accepts the global
options; `--help`/`-h` show help and `--version` shows the executable version.

### version

```console
glesha version
glesha -v
glesha -version
glesha --version
```

All forms print `glesha VERSION (GIT_SHA)` and exit successfully. Development
builds print `glesha dev (unknown)` unless version/revision values are embedded
at build time. Configuration and credential files are not loaded. The version
subcommand accepts the global options; `glesha version --help` or
`glesha help version` lists them. Extra positional arguments are rejected.
See [man/glesha-version.1](man/glesha-version.1).

## Exit status

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Operation failed |
| 2 | Invalid usage or missing selection |
| 3 | Pending work or cold retrieval |
| 4 | Conflicting remote history |
