# glesha CLI reference

Create a standalone encrypted archive, or register a named set to keep a history
of cloud backups. The executable performs compression, encryption and transfers
internally. Full archives also work with ordinary GPG and tar.

Run `glesha help` for a command summary or `glesha help run` for complete options
and examples. `glesha run --help` shows the same command help. Installed manual
pages are named `glesha(1)` and `glesha-run(1)`, with one page per command.
Source pages are in [man/](man/). Linux release archives include the command manuals.

## Back up and recover a directory

```console
glesha create docs ~/Documents --to b2
glesha run docs --env /secure/cloud.env
glesha run docs --incremental --env /secure/cloud.env
glesha history docs
glesha browse docs --path Documents --recursive
glesha restore docs --into ~/recovered --env /secure/cloud.env
```

`create` registers sources and publishes initial metadata for remote-backed sets.
`run` creates a full backup by default;
`--incremental` must be explicit. Give destinations at creation or run time:
there is no default provider. A set has a stable UUID and a local name;
each snapshot has its own UUID. Bound source directories cannot be replaced.
An incremental set keeps its complete history on one provider. `move` copies
and verifies the entire history before switching, retaining original copies.

## Output and progress

Results and tables go to stdout. Activity, prompts, warnings, and errors go to
stderr. Successful commands report a result; empty inventories show a next step.
`--format=json` keeps stdout free of prose, color, and progress.

Informational text is plain. Errors are red, warnings and states requiring action
are yellow, active work is cyan, and synced or ready states are green when color
is enabled. Repeated shared failures are grouped; final errors keep the exit code
without repeating a report already printed.

Human command output uses ASCII. Unicode names are shown with escapes without
changing stored names or JSON values. Paths and labels escape terminal controls. Help has wrapped descriptions, meaningful
argument names and task-based option groups. `glesha help catalog ACTION`
shows focused options for sync, unlock, resolve, and compatibility actions.

`--color=auto` is the default. Use `--color=yes` to force color or `--color=no`
to disable it. Existing `always` and `never` spellings remain supported.
`--log-level=debug` enables operation and worker-budget diagnostics. Normal output
shows compact binary sizes such as `14.20M`; JSON retains integer bytes and its versioned
structure.

Interactive stderr marks steps with `[+]` and shows a progress bar with compact
counters: `[63.35%] [1.27G/2.34G] [1.22M/s]`. Units use binary scaling and two
decimal places. Explicit worker flags add `using N workers` to relevant steps;
counts reflect the operation and its effective limits. Archiving and individual
downloads currently use one worker.
Scanning uses an ASCII spinner (`-`, `\`, `|`, `/`). Archiving shows processed/total
uncompressed file bytes and a percentage, counting hard-linked content once.
Incrementals count only included content. Finalization and metadata publication
show status without an indefinite bar. Upload counts include in-flight payload bytes; SDK checksum reads and retries
do not count the same bytes twice. A 100% transfer still waits for verification
and catalog publication. One renderer refreshes every 100 ms and uses about 80% of terminal width;
worker counters perform no terminal I/O. Redirected stderr and `TERM=dumb` use plain step lines.
`--progress=off` disables progress; `warn`, `error` and `silent` logging also suppress it.
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

Use `--jobs N` or `-j N` as aliases for `--transfer-workers N`: **up to N**
concurrent transfer workers, not a required count. Part count, shared transfer
capacity and memory limits can reduce it.
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
  temporary readable copy automatically, after three days by default. Override with
  `--days N` or `[cold] days = N`; pending operations keep their saved duration.

## Structured output

Every command accepts `--format=json` (or `--format json`), before or after the
command. `--format=plain` is the
default; `status` also supports `table` and `csv`.

- **stdout:** JSON Lines results, retaining version 2 `{version, kind, data}` envelopes.
  Listings emit one record per item; empty listings emit no records.
- **stderr:** version 1 `{version, kind, data}` diagnostics. `kind` is `progress`,
  `error`, `warning`, `info` or `debug`. No ANSI formatting or inline updates.
- Progress emits phase start/stop and updates once per second per active phase.
  Fields: `id`, `phase`, `state`, `completed_bytes`, `total_bytes` (null if unknown),
  `bytes_per_second` and UTC `timestamp`. IDs are unique within an invocation.
  `STOPPED` means the phase ended, not that the operation succeeded; check results
  and the exit code. `--progress=off` disables progress without suppressing errors.
- Errors contain `code`, `message` and `exit_code`. Codes include `FAILED`,
  `INVALID_ARGUMENT`, `PENDING`, `CONFLICT`, `CONFIRMATION_REQUIRED` and `CANCELED`.
  Exit codes are 1 for failures, 2 for CLI errors, 3 for pending work, 4 for conflicts
  and 130 for cancellation.
  Structured errors are emitted even with silent logging.
- JSON does not approve operations or disable hidden password/confirmation prompts.
  For unattended use, supply passphrase files and the operation's explicit consent
  options. Prompts, when needed, remain on stderr; nonterminal input fails safely.

```console
glesha status --format=json
glesha run docs --format=json --progress=off
glesha help restore --format=json
glesha version --format=json
```

## Credentials and passwords

Nonsecret defaults live in `glesha/config.toml` under the platform user-config
directory. Credentials come from the process environment or an explicit `--env`
file; existing variables take precedence. No sensitive file is rewritten.
Archive passwords use a hidden prompt or `--passphrase-file FILE` (`-` for stdin).
The catalog secret is separate: `GLESHA_CATALOG_PASSPHRASE`, a hidden prompt, or
`--catalog-passphrase-file FILE`. Passwords are never stored. For mixed-password
chains use repeated `--archive-passphrase-file SNAPSHOT_ID=FILE` overrides.

### Local state

Databases, pending work and managed archive copies use a dedicated state directory:

| Platform | State directory |
| --- | --- |
| Linux | `$XDG_DATA_HOME/glesha/state`, or `~/.local/share/glesha/state` |
| macOS | `~/Library/Application Support/glesha/state` |
| Windows | `%LOCALAPPDATA%\glesha\state` |

Override it per config, including for development:

```toml
[catalog]
directory = "/absolute/path/to/worktree/.dev/state"
```

`--config` alone does not isolate state. Set `catalog.directory` too.

### Migrate older state

```sh
glesha state migrate
```

Moves only the old `glesha/state-v2` directory under the platform config directory.
Config and credential files stay untouched. Stop other glesha processes first.
Pending operations, archives and IDs are preserved; original database copies stay
in the new state's `.state-migration-backup` directory. No cloud requests are made.
An interrupted migration can be retried with the same command.

Migration refuses an existing destination or unsupported databases. Source and
destination must share a filesystem; archive bytes are moved without copying.
An explicit `catalog.directory` selects the destination. Existing custom state
locations continue to work without migration.

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

Register a backup set and its local source paths. Remote-backed sets discover
existing state and publish their initial catalog and discovery record; no archive
is created. Local-only sets remain local.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--catalog-to STRING` | catalog remote or local |
| `--catalog-passphrase-file STRING` | catalog passphrase for state publication |
| `--chunked[=false]` | bounded-spool encrypted chunks; false selects one archive |
| `--spool-max SIZE` | chunk spool limit; default 128MiB, supports 128M and 1.1G |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression FORMAT` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--incremental-storage-class STRING` | AWS incremental storage class |
| `--prefix STRING` | glesha or encb |
| `--storage-class STRING` | AWS storage class |
| `--to REMOTE...` | remote names; comma-separated or repeated |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
Portable settings synchronize before changes and publish afterward. Source-folder
bindings remain local to this computer.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--baseline STRING` | explicit snapshot baseline |
| `--catalog-passphrase-file STRING` | catalog passphrase for synchronization and publication |
| `--catalog-to STRING` | catalog remote or local |
| `--chunked[=false]` | bounded-spool encrypted chunks; false selects one archive |
| `--spool-max SIZE` | chunk spool limit; default 128MiB, supports 128M and 1.1G |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression FORMAT` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--incremental-storage-class STRING` | AWS incremental storage class |
| `--prefix STRING` | glesha or encb |
| `--root VALUE` | archive root NAME=source PATH |
| `--storage-class STRING` | AWS storage class |
| `--to REMOTE...` | remote names; comma-separated or repeated |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--archive-mode STRING` | auto, memory or stream |
| `--assume-yes` | approve user-data removal confirmations |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--chunked[=false]` | bounded-spool encrypted chunks; false selects one archive |
| `--spool-max SIZE` | chunk spool limit; default 128MiB, supports 128M and 1.1G |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression FORMAT` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--full` | explicit full snapshot |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--incremental` | require a baseline on one remote |
| `--keep-archive` | retain the local archive after a successful backup |
| `--memory-max SIZE` | managed memory budget |
| `--output FILE` | new output file |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--prefix STRING` | glesha or encb |
| `--storage-class STRING` | AWS storage class |
| `--to REMOTE...` | remote names; comma-separated or repeated |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--archive STRING` | relocated completed archive |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--keep-archive` | retain the local archive after a successful backup |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha retry docs
glesha retry docs --archive /new/location/backup.tar.xz.gpg
```

### list

```text
glesha list [--remote --from REMOTE...] [options]
```

Show cached set names and IDs without cloud requests or password prompts. Use
`catalog sync` for discovery and `status` for a fuller overview. The existing
`list --remote --from REMOTE` spelling remains available but is deprecated.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--from REMOTE...` | ordered source remotes; comma-separated or repeated |
| `-h, --help` | show command help |
| `--remote` | discover remote set descriptors |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha list
glesha catalog sync --from b2 --env /secure/cloud.env
```

### status

```text
glesha status [SET] [options]
```

Show `SET STATE SIZE SNAPSHOTS STORAGE CLASS EST/YR` in left-aligned columns.
Sizes use binary `B/K/M/G/T` units. The dominant storage class holds the most
recorded archive bytes; `status SET` adds a per-class breakdown and source paths.
Overview totals deduplicate identical remote object versions shared by sets. Costs estimate archive and metadata
storage only. State labels use green for `SYNCED`, yellow for unfinished work,
and red for conflicts/errors; `--color` controls coloring.

States: `NEW`, `SYNCED`, `RUNNING`, `PAUSED`, `LOCAL`, `PUBLISHING`, `WAITING`,
`STALE`, `CONFLICT`, `FAILED`. `SYNCED` means no known outstanding work based on
local records, not a live recovery check. A stopped publication is `PAUSED`;
`PUBLISHING` requires a live writer. Outstanding work includes a short next action.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--format STRING` | plain (default), table, csv or json |
| `--from REMOTE...` | ordered source remotes; comma-separated or repeated |
| `-h, --help` | show command help |
| `--refresh` | check live metadata without requesting retrieval |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha status
glesha status docs --refresh --env /secure/cloud.env
glesha status --format csv
glesha status docs --format table
```

Plain status uses local records and cached measurements, without cloud requests.
`--refresh` checks recorded archive metadata and lists current metadata objects;
it does not download or pull catalogs or request cold restoration.

Pending cold imports/restores add a `Retrieval Status` table:

```text
Retrieval Status
Last checked: 2026-10-15 14:32 IST
SET        TASK     STATE    READY  WAITING  EXPIRES (IST)
music      RESTORE  READY    1      0        Oct 18 05:30
photos     RESTORE  PARTIAL  2      1        -
old-music  IMPORT   QUEUED   0      1        -
```

Counts refer to archives; `READY` means downloadable. `EXPIRED` means the readable
copy expired. Expiry is the earliest deadline once all requested archives are ready.
Last checked is the oldest observation, or `-` when unknown. `--refresh` checks
pending imports too, before cataloging. Human timestamps use local time; JSON uses
UTC. JSON includes each operation's ID and object references; CSV appends a
`retrievals` JSON column. Completed operations leave this section.

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
Locked catalogs show `-` for snapshot counts and sizes in human output. Totals
cover readable catalogs; the report identifies partial coverage.
JSON retains its version 2 envelope and original fields; additional byte counts
remain integers and USD estimates are numeric.

CSV has stable columns, integer byte counts and numeric USD values; it omits
totals and prose.

### history

```text
glesha history SET [options]
```

A downloaded locked catalog can be unlocked with `--catalog-passphrase-file FILE`
or a hidden prompt. Archive passwords are not needed to inspect history.

Show completed snapshots, newest first, including transfer and storage status.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--refresh` | check live metadata without requesting retrieval |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha history docs
glesha history docs --refresh --format=json
```

### browse

```text
glesha browse SET [options]
```

Browse a snapshot's catalog metadata. A downloaded catalog can be unlocked
locally; missing metadata is fetched from its authority. No archive data is downloaded.
Use `--catalog-passphrase-file FILE` to unlock without a terminal prompt.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--path STRING` | catalog path |
| `--recursive` | include descendants |
| `--snapshot ID` | snapshot ID or latest (default "latest") |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha browse docs
glesha browse docs --path Documents --recursive
glesha browse docs --snapshot SNAPSHOT_ID --recursive --format=json
```

### restore

```text
glesha restore SET --into DIR [options]
```

Reconstruct a snapshot in a new or existing directory, including `--into .`.
Existing directories are merged while preserving their permissions. Existing
files, symlinks, and conflicting paths are rejected before payload downloads;
files are never overwritten. Interrupted publication preserves verified staging;
repeat the restore command to resume. Cold retrieval requires explicit consent.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--archive-passphrase-file VALUE` | snapshot ID=passphrase file |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--days INT` | readable-copy availability days |
| `--env FILE` | explicit credential environment file |
| `--from REMOTE...` | ordered source remotes; comma-separated or repeated |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--into DIR` | destination directory; existing files are never overwritten |
| `--memory-max SIZE` | managed memory budget |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--request-retrieval` | explicitly authorize retrieval charges |
| `--retrieval STRING` | bulk, standard or expedited (default "bulk") |
| `--snapshot ID` | snapshot ID or latest (default "latest") |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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

Imports automatically publish metadata when a remote catalog destination exists.
Local-only imports remain local. Existing archive objects are not rewritten.
Publication failures retain the completed import: use `retry SET`, or repeat the
same import, to finish publication without downloading/cataloging again.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--baseline STRING` | explicit snapshot baseline |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--date STRING` | known backup date in RFC3339 |
| `--days INT` | readable-copy availability days |
| `--deep-verify` | verify associated object bytes |
| `--env FILE` | explicit credential environment file |
| `--file STRING` | local archive |
| `--from REMOTE...` | ordered source remotes; comma-separated or repeated |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--key STRING` | exact remote object key |
| `--memory-max SIZE` | managed memory budget |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--request-retrieval` | explicitly authorize retrieval charges |
| `--retrieval STRING` | bulk, standard or expedited (default "bulk") |
| `--root VALUE` | archive root NAME=source PATH |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--assume-yes` | approve user-data removal confirmations |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--days INT` | readable-copy availability days |
| `--env FILE` | explicit credential environment file |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--memory-max SIZE` | managed memory budget |
| `--request-retrieval` | explicitly authorize retrieval charges |
| `--retrieval STRING` | bulk, standard or expedited (default "bulk") |
| `--to REMOTE...` | remote names; comma-separated or repeated |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha move docs --from aws --to b2
glesha move docs --from aws --to other-remote --request-retrieval --retrieval bulk
```

### archive

```text
glesha archive PATH... [options]
```

Create a standalone encrypted tar archive. No set name or cloud provider is needed.

| Option | Meaning |
| --- | --- |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--archive-mode STRING` | auto, memory or stream |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--compression FORMAT` | xz, gzip or bzip2 |
| `--compression-level INT` | compression level 1..9 |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--memory-max SIZE` | managed memory budget |
| `--output FILE` | new output file |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--prefix STRING` | glesha or encb |
| `--storage-class STRING` | AWS storage class |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--memory-max SIZE` | managed memory budget |
| `--output FILE` | new output file |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--into DIR` | new destination directory |
| `--memory-max SIZE` | managed memory budget |
| `--passphrase-file STRING` | passphrase file; '-' reads stdin |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--force` | explicitly replace an existing remote key |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--key STRING` | remote key (default filename) |
| `--memory-max SIZE` | managed memory budget |
| `--storage-class STRING` | AWS storage class |
| `--to REMOTE...` | remote names; comma-separated or repeated |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--from REMOTE...` | ordered source remotes; comma-separated or repeated |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--memory-max SIZE` | managed memory budget |
| `--output FILE` | new output file |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha download backups/personal.tar.xz.gpg --from b2,aws
glesha download backups/personal.tar.xz.gpg --from b2 --output personal.tar.xz.gpg
```

### catalog

```text
glesha catalog sync [SET] [--from REMOTE...] [options]
glesha catalog unlock SET [options]
glesha catalog resolve SET --keep local|remote [--assume-yes]
glesha catalog resolve SET_UUID --authority REMOTE [--assume-yes]
glesha catalog pull|push|reconcile|abandon SET [options]
```

Bare `sync` discovers configured buckets, reconstructs the local registry, and
downloads encrypted catalog state without passphrase prompts. Names remain
private: locked sets appear by UUID. `--all` is an alias for this discovery flow;
`--from` and `--to` limit all bucket access to their combined selection.
Following a catalog locator outside this selection fails before contacting that bucket. Other sets continue after individual failures;
incomplete discovery and failed registry replicas produce a nonzero exit status.

`unlock SET` reads an already downloaded catalog locally using its catalog
passphrase, with no cloud requests. A missing cache directs you to `catalog sync`.
It emits a version 2 `catalog_unlocked` record under `--format=json`, with `set`, `name`,
and `already_open` fields.

`sync SET` unlocks that catalog, pulls remote changes, publishes local changes,
and merges compatible histories. Conflicts require explicit resolution. Migrated
state uses publication version 3; all writing devices must upgrade. Original
metadata and archive bytes are preserved. Sync does not inspect archive payloads
or request cold retrieval. Use `status --refresh` for live archive observations.
Human output reports **Updated** only when catalog state was downloaded, repaired,
unlocked into a usable local catalog, or a revision changed. An unchanged run
prints `Everything is up to date`, with counts of checked and locked catalogs.
Locked means opening the latest catalog requires its passphrase; it does not mean
sync failed or that data was downloaded. Shared failures are grouped by cause with catalog counts in human output.
Failure details and incomplete summaries use red when color is enabled. JSON
retains individual catalog errors; discovery and registry errors use
`catalog_sync_error` records. The final terminal error does not repeat details.
Routine cached checks are quiet. Sync
still makes metadata API requests to check for remote changes.

An unchanged catalog does not publish another batch. Failed registry replicas
are repaired by repeating sync without repeating completed archive uploads.

`catalog resolve SET` previews differing references and incremental starting snapshots.
To apply a choice, use `resolve --keep local|remote`. This selects which
side's references and incremental starting snapshot to keep while retaining both
snapshot histories and preserving the previous local branch. Resolution requires
`[y/N]`; `--assume-yes` is the automation override. Different archive identities
are never silently replaced. Concurrent remote writers still cause a conflict.
No blanket `--force` is supported.

Existing pull/push/reconcile/abandon commands remain supported. Pull preserves the
old local catalog; abandon preserves pending data.

| Option | Meaning |
| --- | --- |
| `--all` | alias for bare sync: discover configured buckets and download state |
| `--from REMOTE...` | limit bucket access; comma-separated or repeated |
| `--authority REMOTE` | explicitly select a discovered catalog authority during resolve |
| `--keep STRING` | choose local or remote conflicting metadata during resolve |
| `--assume-yes` | confirm conflict resolution without prompting |
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha catalog sync
glesha status
glesha catalog sync SET_UUID
glesha catalog sync --from b2
glesha catalog resolve docs --keep remote
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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--catalog-passphrase-file STRING` | separate catalog passphrase file |
| `--class STRING` | AWS storage class |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `-h, --help` | show command help |
| `--snapshot ID` | snapshot ID or latest (default "latest") |
| `--storage-class STRING` | AWS storage class |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

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
| `-L, --log-level LEVEL` | diagnostic verbosity: debug, info, warn, error, or silent |
| `--color STRING` | yes, no or auto; always/never aliases accepted (default "auto") |
| `--config FILE` | TOML configuration file |
| `--state-dir DIR` | local state directory; overrides catalog.directory |
| `--env FILE` | explicit credential environment file |
| `--hash-workers INT` | hash workers |
| `-h, --help` | show command help |
| `--memory-max SIZE` | managed memory budget |
| `--to REMOTE...` | remote names; comma-separated or repeated |
| `-j, --jobs, --transfer-workers N` | maximum parallel transfers; memory may lower this limit |
| `-j N`, `--jobs N` | use up to N transfer workers; effective limits may reduce the count |
| `--version`, `-version`, `-v` | Show version and build revision. |
| `--format FORMAT` | plain (default) or json; status also supports table/csv |
| `--progress MODE` | auto (default), plain or off |

Examples:

```console
glesha check --to b2 --env /secure/cloud.env
glesha check --to aws,b2 --env /secure/cloud.env
glesha check --to b2 --format=json --env /secure/cloud.env
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

## Bounded disk space

`glesha run docs --chunked --spool-max 128M` creates independently encrypted
chunks instead of one archive. `128M` and `1.1G` use decimal units; `128MiB` uses
binary units. The default spool limit is 128MiB. `create` and `configure` can save
these settings; `run --chunked=false` selects a single archive.

Chunked runs reject `--output` and `--keep-archive`. Completed chunks are uploaded
and removed locally before continuing. Retrying re-reads the source and checks
completed chunks, then uploads remaining work; changed sources cause an error. Repeat `run SET`
with the original archive passphrase to resume unfinished chunks. `retry SET` can
finish publication once all payload chunks and the descriptor are ready.
A chunk contains a fragment of the tar stream. For manual recovery, decrypt the
manifest, decrypt and decompress its chunks in order, then concatenate the
uncompressed fragments before extracting the tar stream.

The encrypted `manifest.json.gpg` records chunk order, sizes and checksums. Keep
it with the chunks for recovery. Single-file and chunked snapshots can share a
history. `move`, `storage-class`, raw `archive` and raw archive `import` currently
accept single-file snapshots only. Failed runs can leave uncommitted remote
chunks; glesha does not delete these automatically.

### cleanup

```text
glesha cleanup SET|--all [--dry-run] [--assume-yes] [global options]
```

Remove managed local `import-*.gpg` and `payload-*.gpg` copies after successful
publication. Preview lists files and reclaimable bytes, then asks `[y/N]`.
`--dry-run` previews only; `--assume-yes` skips confirmation, never eligibility
checks. `--env` and `--config` select the normal credentials/configuration.

Skip entire sets with pending operations, unfinished imports/restores, incomplete
snapshots, unpublished changes or no published catalog. Each candidate must match
its recorded hash/size and have an exact remote copy verified with metadata
requests, plus recorded content verification or a completed pinned import receipt.
Metadata-only associations are insufficient without a matching remote hash.
Revalidate before removal. No archive downloads or cold retrievals.

Keep catalogs, registry, configuration, sources, extracted data, external archives
and explicitly selected backup outputs. Remote objects are never deleted. Future
recovery may require charged retrieval/downloads again.

```console
glesha cleanup docs --dry-run --env /secure/cloud.env
glesha cleanup docs --env /secure/cloud.env
glesha cleanup --all --assume-yes --env /secure/cloud.env
```

## Remote access boundaries

When neither --from nor --to is supplied, all configured providers are allowed. Otherwise their combined selection is a strict bucket access boundary for archive operations, discovery, catalogs, migration and registry replication. A required provider outside that boundary causes an error; include it explicitly with --from or --to. These flags do not make commands read-only.

Both flags are accepted by commands that access remote state. Existing source and destination semantics remain: `--to` chooses archive destinations where applicable, and `--from` chooses archive sources where applicable. On other commands either flag adds permitted providers. For example, moving from A to B requires `glesha move SET --from A --to B`; backing up to B with a catalog in A requires `glesha run SET --from A --to B`. Bare scoped catalog sync skips locally known sets whose catalog authority is outside the selection; explicitly targeting such a set fails.

## Choosing local state

`--state-dir DIR` overrides `[catalog] directory` for this invocation and expands
`~`. It works across commands and does not select or isolate cloud buckets.
For development, use a separate state directory and test buckets.

An empty legacy directory or supported empty registry does not block the platform
default state directory. `state migrate` reports that no legacy backups need
migration and leaves it untouched. If both locations contain state, choose one
explicitly using `--state-dir`, or migrate the legacy state to an unused
`--state-dir` destination. Existing databases are never automatically merged.

Catalog sync announces `Checking remote catalogs` before remote checks. Terminals
show an activity indicator; redirected human output gets a starting line and
summary. JSON and warn/error/silent logging levels suppress this activity message.

## Cancelling unfinished restores

`glesha restore SET --cancel` clears local unfinished restore tasks for that set.
Use `--into DIR` to select one destination. It preserves restored files, cached
archives and staging data, and retains task records with a `.cancelled-UUID`
suffix. It requires no catalog unlock or cloud access and does not stop cloud
retrieval requests already submitted. Repeating cancellation is harmless.

Catalog history is applied under one `Applying catalog updates` progress step;
per-batch cache verification remains quiet. Interactive passphrase reads observe
Ctrl+C without requiring Enter, restore terminal settings before returning, and
report cancellation with exit code 130.

The `list`, `history`, and `browse` human formats have headers and explicit empty
results. Directory entries use `-` for size. Snapshot locations use readable
provider labels instead of opaque remote IDs. JSON retains original identifiers
and record fields. Empty and no-change JSON commands do not print human prose.
