# CLI reference

`glesha help COMMAND` and `man glesha-COMMAND` show the same information offline.

## Command syntax

```text
glesha [global options] COMMAND [arguments] [options]
```

These rules apply to every command:

- Options can appear before or after positional arguments.
- Global options can appear before or after the command name.
- An option value can follow an equals sign or a space: `--format=json` and `--format json` are the same.
- `--` ends option parsing. Use it for a path that starts with `-`.
- `glesha COMMAND --help` prints the same text as `glesha help COMMAND`.

### Value types

| Placeholder | Meaning |
| --- | --- |
| `SET` | A set name or a set UUID. |
| `REMOTE...` | One or more remote names. Separate them with commas, or repeat the option: `--to aws,b2` is the same as `--to aws --to b2`. Duplicates are removed, and the order is kept. |
| `SNAPSHOT`, `ID` | A snapshot UUID. Where the option allows it, `latest` selects the newest snapshot. |
| `SIZE` | A whole number with an optional unit: `KiB`, `MiB`, `GiB`, `KB`, `MB`, or `GB`. `--spool-max` also accepts `K`, `M`, and `G` as decimal units. |
| `FILE\|-` | A file path, or `-` for standard input. |

### Bucket access

When a command receives `--from`, `--to`, or both, glesha accesses only the buckets that those options name. A command that needs any other bucket fails before it contacts that bucket.

The meaning of each option depends on the command:

- `--to` names destinations for `create`, `configure`, `run`, `move`, `upload`, and `check`. For other commands, it only adds buckets that the command may access.
- `--from` names sources in fallback order for `restore`, `import`, and `download`. For other commands, it names the buckets that the command may read.

## Global options

Every command accepts these options.

| Option | Description |
| --- | --- |
| `--config FILE` | TOML configuration file. The default path is in [Configuration](configuration.md#configuration-file). |
| `--state-dir DIR` | Local state directory. Overrides `[catalog] directory`. |
| `--env FILE` | Load environment variables from `FILE`. Variables already set in the environment keep their values. |
| `--format FORMAT` | Result format: `plain` or `json`. `status` also accepts `table` and `csv`. Default: `plain`. |
| `--color MODE` | `auto`, `yes`, or `no`. Default: `auto`. |
| `-L`, `--log-level LEVEL` | `debug`, `info`, `warn`, `error`, or `silent`. Default: `info`. |
| `--progress MODE` | Progress on standard error: `auto`, `plain`, or `off`. Default: `auto`. |
| `-h`, `--help` | Print help for the command. |
| `-v`, `--version` | Print the version and build revision. |

## Shared option groups

### Performance options

| Option | Description |
| --- | --- |
| `--memory-max SIZE` | Memory budget for the command. Overrides `[memory] max`. Default: half of the available memory on Linux, 128 MiB on other systems. Minimum: 48 MiB. |
| `-j N`, `--jobs N`, `--transfer-workers N` | Maximum number of parallel transfers. Overrides `[workers] transfer`. The memory budget can lower the effective number. |
| `--hash-workers N` | Maximum number of parallel file hashing workers. Overrides `[workers] hash`. |

### Retrieval options

For archives in AWS cold storage.

| Option | Description |
| --- | --- |
| `--request-retrieval` | Allow glesha to request retrievals, which AWS charges for. Without this option, glesha asks first. |
| `--retrieval TIER` | `bulk`, `standard`, or `expedited`. Default: `[cold] tier`, which is `Bulk` unless you change it. |
| `--days N` | Number of days that the readable copy stays available. Default: `[cold] days`, which is `3` unless you change it. A pending retrieval keeps the duration it started with. |

### Archive options

| Option | Description |
| --- | --- |
| `--compression FORMAT` | `xz`, `gzip`, or `bzip2`. |
| `--compression-level LEVEL` | `1` to `9`. |
| `--prefix PREFIX` | Prefix for new archive names: `glesha` or `encb`. |
| `--chunked` | Upload the archive as independent encrypted chunks. `--chunked=false` selects a single archive. |
| `--spool-max SIZE` | Maximum chunk data on local disk. Requires `--chunked`. Default: 128 MiB. Minimum: 2 MiB. |

## Commands

| Command | Description |
| --- | --- |
| [`create`](#create) | Register a backup set. |
| [`configure`](#configure) | Change the saved settings of a set or bind its roots to local directories. |
| [`run`](#run) | Create and upload a full or incremental backup. |
| [`retry`](#retry) | Resume a pending operation. |
| [`restore`](#restore) | Restore a snapshot, or cancel unfinished restore tasks. |
| [`import`](#import) | Register an existing full archive as a snapshot. |
| [`list`](#list) | List locally known sets. |
| [`status`](#status) | Show the state, size, and estimated cost of sets. |
| [`history`](#history) | List the snapshots of a set. |
| [`browse`](#browse) | List the files in a snapshot. |
| [`catalog`](#catalog) | Discover, unlock, synchronize, or repair catalogs. |
| [`cleanup`](#cleanup) | Remove local archive copies that also exist on a remote. |
| [`move`](#move) | Copy a set to another remote and switch its destination. |
| [`storage-class`](#storage-class) | Change the AWS storage class of a snapshot. |
| [`check`](#check) | Test upload, download, and delete access to buckets. |
| [`state`](#state) | Move state from an older location. |
| [`archive`](#archive) | Create a standalone encrypted archive. |
| [`decrypt`](#decrypt) | Decrypt an archive to a compressed tar file. |
| [`extract`](#extract) | Extract an archive into a new directory. |
| [`upload`](#upload) | Upload a file to buckets. |
| [`download`](#download) | Download a file from buckets. |
| [`help`](#help) | Show help for a command. |
| [`version`](#version) | Show the version and build revision. |

### create

```text
glesha create SET PATH... [options]
```

Registers a new set named `SET` with one or more source directories. Each `PATH` becomes a root named after the last part of the path.

For a set with a remote catalog, `create` looks for existing state in the bucket and publishes an initial catalog. It does not create a snapshot. Run `glesha run SET` for the first backup.

| Option | Description |
| --- | --- |
| `--to REMOTE...` | Destination remotes. Without this option, the first `run --to` saves its remotes as the destinations. |
| `--from REMOTE...` | Other buckets that the command may access. |
| `--catalog-to REMOTE\|local` | Catalog location. Default: the first remote in `--to`. `local` keeps the catalog only on this computer. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |
| `--storage-class CLASS` | AWS storage class for snapshots. Default: `[backup] full_storage_class`. |
| `--incremental-storage-class CLASS` | AWS storage class for incremental snapshots. Default: `[backup] incremental_storage_class`, then `--storage-class`. |

Also accepts the [archive options](#archive-options). The values are saved with the set.

```sh
glesha create docs ~/Documents --to b2
glesha create photos ~/Pictures --to aws,b2 --compression gzip
```

### configure

```text
glesha configure SET [options]
```

Changes the saved settings of a set, or binds its roots to directories on this computer. Settings that are stored in the catalog are synchronized before the change and published after it. Root bindings stay on this computer.

A root that is already bound cannot be bound to a different directory. The destinations of a set with incremental snapshots, and a catalog location that is already published, can be changed only with [`move`](#move). `configure` fails while the set has pending work.

| Option | Description |
| --- | --- |
| `--root NAME=PATH` | Bind the root `NAME` to the directory `PATH`. Repeat the option for each root. |
| `--baseline ID` | Make snapshot `ID` the baseline of the next incremental backup. The snapshot must be complete. |
| `--to REMOTE...` | Destination remotes. |
| `--from REMOTE...` | Other buckets that the command may access. |
| `--catalog-to REMOTE\|local` | Catalog location. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |
| `--storage-class CLASS` | AWS storage class for snapshots. |
| `--incremental-storage-class CLASS` | AWS storage class for incremental snapshots. |

Also accepts the [archive options](#archive-options). A new compression setting applies to future snapshots only.

```sh
glesha configure docs --compression gzip
glesha configure docs --root Documents=~/Documents
glesha configure imported --root Documents=~/Documents --to b2 --catalog-to b2
```

### run

```text
glesha run SET [--full | --incremental] [options]
```

Creates a snapshot of the set's roots and uploads it to the set's destinations.

`run` creates a full snapshot unless you pass `--incremental`. An incremental snapshot needs an earlier snapshot to build on and exactly one destination remote. That remote must hold every earlier snapshot of the set. After the first incremental snapshot, the set is bound to that remote, and every later run must use it. Use `move` to switch remotes.

If no file changed since the previous snapshot, `run` creates no snapshot and prints `No changes in "SET"; no snapshot created.`

If an incremental run finds removed paths or paths that changed type, glesha lists them and asks for confirmation. `--assume-yes` approves them.

If a previous run of the set did not finish, `run` resumes it with the original settings. Options that you pass must match the interrupted run, and the archive passphrase must be the same.

glesha deletes an automatically named local archive after every upload and the catalog publication succeed. It keeps the archive if you pass `--keep-archive` or `--output`, or if the run fails.

| Option | Description |
| --- | --- |
| `--full` | Create a full snapshot. This is the default. |
| `--incremental` | Create an incremental snapshot. |
| `--output FILE` | Write the archive to `FILE`, which must not exist, and keep it. |
| `--keep-archive` | Keep the local archive after a successful backup. |
| `--archive-mode MODE` | `auto`, `memory`, or `stream`. See [Archive modes](configuration.md#archive-modes). |
| `--to REMOTE...` | Destination remotes for this run. |
| `--from REMOTE...` | Other buckets that the command may access. |
| `--storage-class CLASS` | AWS storage class for this snapshot. |
| `--passphrase-file FILE\|-` | Archive passphrase. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |
| `--assume-yes` | Answer yes to confirmation prompts. |

Also accepts the [archive options](#archive-options) and the [performance options](#performance-options). A chunked run does not accept `--output`, `--keep-archive`, or `--archive-mode memory`.

```sh
glesha run docs
glesha run docs --incremental
glesha run docs --chunked --spool-max 128M
```

### retry

```text
glesha retry SET [options]
```

Resumes the pending operation of a set. The operation can be a backup, a move, a catalog publication, or a storage-class change. `retry` also repairs registry copies. Finished uploads are reused, and the original cleanup choice is kept.

| Option | Description |
| --- | --- |
| `--archive FILE` | The new location of a finished archive that was moved. glesha keeps that file after the operation. Not available for chunked runs. |
| `--keep-archive` | Keep the local archive after a successful backup. Not available for chunked runs. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--from REMOTE...` | Other buckets that the command may access. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

```sh
glesha retry docs
glesha retry docs --archive /new/location/backup.tar.xz.gpg
```

### restore

```text
glesha restore SET --into DIR [options]
glesha restore SET --cancel [--into DIR] [options]
```

Restores a snapshot into `DIR`. `DIR` can be new or existing, and `--into .` is allowed. Existing directories are merged, and their permissions are kept. If any path that glesha would write already exists, the restore stops before it downloads archive data.

If a restore stops, run the same command again. glesha keeps the data it already verified.

If the snapshot needs archives in AWS cold storage, glesha asks for consent to request a retrieval. While the retrieval is pending, `restore` exits with status 3. Run the command again when the archives are ready.

With `--cancel`, glesha clears unfinished local restore tasks for the set. With `--into DIR` as well, it clears only the task for that destination. Restored files and staged data stay on disk. Retrievals that were already requested from AWS continue.

| Option | Description |
| --- | --- |
| `--into DIR` | Destination directory. With `--cancel`, selects the task to cancel. |
| `--snapshot ID\|latest` | Snapshot to restore. Default: `latest`. |
| `--cancel` | Cancel unfinished local restore tasks. |
| `--from REMOTE...` | Remotes to read archives from, in the order to try them. Default: the set's destinations. Local archive copies are used first when they exist. A readable copy is used before a copy that needs a retrieval. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--passphrase-file FILE\|-` | Archive passphrase. |
| `--archive-passphrase-file ID=FILE` | Archive passphrase for one snapshot. Repeat for each snapshot whose passphrase differs. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

Also accepts the [retrieval options](#retrieval-options) and the [performance options](#performance-options).

```sh
glesha restore docs --into ~/recovered
glesha restore docs --snapshot SNAPSHOT_ID --into ~/recovered --from b2,aws
glesha restore docs --into ~/recovered --request-retrieval --retrieval bulk
glesha restore docs --cancel
```

### import

```text
glesha import SET --file FILE [options]
glesha import SET --from REMOTE --key KEY [options]
```

Adds an existing full archive to the set `SET` as a snapshot. If `SET` does not exist, glesha creates it. glesha reads the archive to build the catalog. It does not change the archive or upload it again.

With `--from` and `--key`, glesha downloads the object once and records the remote copy. With `--file` only, the set stays local. With `--file` and `--from`, glesha reads the local file and records the remote object `--key` as its copy, after it checks that the sizes match. Without `--key`, the key is the file name.

If the set has a remote catalog location, `import` publishes the catalog. If the publication fails, the import is kept. Run `glesha retry SET` or the same `import` command again to finish it.

| Option | Description |
| --- | --- |
| `--file FILE` | Local archive. |
| `--key KEY` | Exact object key of the archive in the remote. With `--file`, the default is the file name. |
| `--from REMOTE...` | Remotes to read the archive from, in the order to try them. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--root NAME=PATH` | Bind a root to a local directory. |
| `--baseline new` | Make the imported snapshot the baseline of the next incremental backup. `new` is the only accepted value. To select another snapshot, use `configure --baseline ID`. |
| `--date RFC3339` | Original backup date, for example `2024-01-02T03:04:05Z`. Without it, the date is unknown. |
| `--deep-verify` | With `--file` and `--from`, also download the remote object and check that its checksum matches the file. |
| `--passphrase-file FILE\|-` | Archive passphrase. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

Also accepts the [retrieval options](#retrieval-options) and the [performance options](#performance-options).

```sh
glesha import old-docs --file old-backup.tar.gz.gpg
glesha import old-docs --from b2 --key backups/old-backup.tar.gz.gpg
glesha import old-docs --file old-backup.tar.gz.gpg --from b2 --key backups/old-backup.tar.gz.gpg --deep-verify
```

### list

```text
glesha list [options]
```

Lists the sets in the local registry with their UUIDs. A set with a locked catalog shows `(locked catalog)` in place of its name. `list` makes no cloud requests and asks for no passphrase. To find sets that exist only in a bucket, run `glesha catalog sync` first.

| Option | Description |
| --- | --- |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |
| `--remote` | Deprecated. Use `glesha catalog sync`, then `glesha list`. |

```sh
glesha list
```

```text
SET                             ID
docs                            87156a05-ea28-45fe-81df-bb74a4a50f3a
```

### status

```text
glesha status [SET] [options]
```

Shows one line for each set, or details for one set. By default, `status` reads local records only and makes no cloud requests. With `--refresh`, it checks the archive objects and lists the catalog objects in each bucket. It never downloads catalogs or requests retrievals.

```text
SET    STATE   SIZE   SNAPSHOTS  STORAGE CLASS  EST/YR
docs   SYNCED  1.65K  2          B2             <$0.01
TOTAL          1.65K  2                         <$0.01
```

| Column | Meaning |
| --- | --- |
| `SET` | Set name, or UUID if the catalog is locked. |
| `STATE` | One of the [set states](#set-states). |
| `SIZE` | Archive size, in binary units. |
| `SNAPSHOTS` | Number of snapshots. |
| `STORAGE CLASS` | The storage class that holds the most archive bytes. |
| `EST/YR` | Estimated storage cost in US dollars per year. |

A `-` means that the value is unknown. It does not mean zero.

`status SET` also shows the set UUID, catalog state, last backup and sync times, destinations, root bindings, and the share of bytes in each storage class. Sets with outstanding work are listed under **Needs attention** with a suggested next step.

If an import or restore is waiting for a cold-storage retrieval, a **Retrievals** table follows. Without `SET`, its first column is `SET`.

| Column | Meaning |
| --- | --- |
| `TASK` | `RESTORE` or `IMPORT`. |
| `STATE` | Progress of the retrieval, for example `QUEUED`, `PARTIAL`, `READY`, or `EXPIRED`. |
| `READY` | Number of archives that can be downloaded. |
| `WAITING` | Number of archives that are not ready yet. |
| `EXPIRES` | When the first readable copy expires, once every archive is ready. |

The cost estimate covers archive and catalog storage for twelve months at current size, using prices embedded in glesha. It leaves out request fees, retrieval fees, egress, temporary readable copies, taxes, discounts, and free tiers. An asterisk (`*`) marks an estimate that is missing some sizes or prices.

| Option | Description |
| --- | --- |
| `--refresh` | Check remote objects. Does not request retrievals. |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |

`status` accepts two extra formats: `--format table` and `--format csv`. CSV has fixed columns, byte counts as integers, and prices as numbers, with no totals.

```sh
glesha status
glesha status docs --refresh
glesha status --format csv
```

### history

```text
glesha history SET [options]
```

Lists the completed snapshots of a set, newest first. `history` reads the local catalog. If the catalog is encrypted and not open on this computer, glesha downloads it if needed and asks for the catalog passphrase. The archive passphrase is not needed.

```text
SNAPSHOT                              CREATED               TYPE               SIZE  COPIES
b0e70271-72a4-4c79-a6fa-a9cf03eea029  2026-11-21 12:09 IST  incremental        843B  b2: readable (STANDARD)
7313fb03-1fab-48d6-89ee-9e10adb21b70  2026-11-21 12:09 IST  full               847B  b2: readable (STANDARD)
```

For an imported snapshot, `CREATED` shows the `--date` given at import, or `-`.

| Option | Description |
| --- | --- |
| `--refresh` | Check whether each archive copy is readable. Does not request retrievals. |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

```sh
glesha history docs
glesha history docs --refresh --format json
```

### browse

```text
glesha browse SET [options]
```

Lists the entries of a snapshot from the local catalog. No archive data is downloaded. By default, `browse` shows the top level of the latest snapshot.

```text
TYPE              SIZE  PATH
directory            -  Documents
directory            -  Documents/notes
file                8B  Documents/c.txt
file                6B  Documents/notes/a.txt
```

| Option | Description |
| --- | --- |
| `--snapshot ID\|latest` | Snapshot to list. Default: `latest`. |
| `--path PATH` | Catalog path to list, for example `Documents/notes`. |
| `--recursive` | Include every entry below the path. |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

```sh
glesha browse docs --recursive
glesha browse docs --path Documents --snapshot SNAPSHOT_ID
```

### catalog

```text
glesha catalog ACTION [SET] [options]
```

Manages catalogs. No `catalog` action restores files or requests retrievals.

| Action | Description |
| --- | --- |
| [`sync [SET]`](#catalog-sync) | Without `SET`, discover sets and download their catalogs. With `SET`, synchronize that catalog in both directions. |
| [`unlock SET`](#catalog-unlock) | Open a downloaded catalog with its passphrase. |
| [`resolve SET`](#catalog-resolve) | Review or resolve a conflict. |
| `pull SET` | Replace the local catalog with the remote catalog. The previous local catalog is kept. |
| `push SET` | Publish local catalog changes. |
| `reconcile SET` | Merge compatible catalog histories. |
| `abandon SET` | Discard the local work of a pending operation. The catalog and remote objects are kept. Chunk data waiting on disk can be removed. |

`pull`, `push`, and `reconcile` accept `--from`, `--to`, and `--catalog-passphrase-file`. `abandon` accepts only the global options. `glesha catalog sync SET` performs `pull`, `reconcile`, and `push` together.

#### catalog sync

```text
glesha catalog sync [SET] [options]
```

Without `SET`, glesha searches the configured buckets for sets, rebuilds the local registry, and downloads each catalog. It asks for no passphrase. Encrypted catalogs appear by UUID and are reported as locked. If one set fails, glesha continues with the others and exits with a non-zero status at the end.

With `SET`, glesha opens the catalog, downloads remote changes, merges compatible histories, and publishes local changes. This form can write to the bucket. If the histories conflict, sync stops and you must run [`catalog resolve`](#catalog-resolve).

glesha prints one row for each set that it updated:

| Result | Meaning |
| --- | --- |
| `Updated` | glesha downloaded, repaired, or unlocked the catalog, or its revision changed. |
| `Updated (locked)` | glesha downloaded the catalog, and it needs the catalog passphrase to open. |

A summary line follows, for example `Catalog sync complete. 1 updated; 1 locked.` If a set failed, the line starts with `Catalog sync incomplete.` If nothing changed, glesha prints `Everything is up to date.` and the number of catalogs that it checked.

| Option | Description |
| --- | --- |
| `--from REMOTE...` | Buckets to search or access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--all` | The same as `sync` without `SET`. Searches only the allowed buckets. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

```sh
glesha catalog sync
glesha catalog sync --from b2
glesha catalog sync docs
```

#### catalog unlock

```text
glesha catalog unlock SET [options]
```

Opens a catalog that `catalog sync` already downloaded. glesha uses only the local copy and makes no cloud requests. After unlocking, the set has its name again. Archives stay encrypted.

| Option | Description |
| --- | --- |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

```sh
glesha catalog unlock 87156a05-ea28-45fe-81df-bb74a4a50f3a
```

#### catalog resolve

```text
glesha catalog resolve SET [options]
glesha catalog resolve SET --keep local|remote [options]
glesha catalog resolve SET --authority REMOTE [options]
```

Without `--keep` or `--authority`, glesha prints the differences between the local and remote catalog. With `--keep`, it keeps the references and incremental baseline of one side. Both snapshot histories are kept in either case. glesha asks for confirmation before it changes anything.

| Option | Description |
| --- | --- |
| `--keep local\|remote` | Keep the local or the remote side. |
| `--authority REMOTE` | Select the remote that holds the authoritative catalog. |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |
| `--assume-yes` | Answer yes to the confirmation prompt. |

```sh
glesha catalog resolve docs
glesha catalog resolve docs --keep remote
```

### cleanup

```text
glesha cleanup SET [options]
glesha cleanup --all [options]
```

Removes local archive copies in the state directory when a verified copy exists on a remote. Remote backups and files outside the state directory are never removed. glesha lists the files and asks for confirmation before it removes them.

glesha skips a set that is in the `CONFLICT`, `FAILED`, `PAUSED`, or `WAITING` state, or that has an unfinished snapshot.

`cleanup` reads the catalog passphrase from `GLESHA_CATALOG_PASSPHRASE` or from a prompt. It does not accept `--catalog-passphrase-file`.

| Option | Description |
| --- | --- |
| `--all` | Process every set in the local registry. |
| `--dry-run` | List the files that would be removed, and remove nothing. |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--assume-yes` | Answer yes to the confirmation prompt. |

```sh
glesha cleanup docs --dry-run
glesha cleanup docs
```

### move

```text
glesha move SET --from SOURCE --to DESTINATION [options]
```

Copies every snapshot of the set from `SOURCE` to `DESTINATION`, verifies the copies, and makes `DESTINATION` the set's destination for future backups. The copies on `SOURCE` are kept. Name every source and catalog bucket in `--from`.

`move` does not support chunked snapshots.

| Option | Description |
| --- | --- |
| `--from REMOTE...` | Source and catalog buckets. |
| `--to REMOTE...` | Destination remotes. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |
| `--assume-yes` | Answer yes to confirmation prompts. |

Also accepts the [retrieval options](#retrieval-options) and the [performance options](#performance-options).

```sh
glesha move docs --from aws --to b2
glesha move docs --from aws --to offsite --request-retrieval
```

### storage-class

```text
glesha storage-class SET --class CLASS [options]
```

Changes the AWS storage class of one snapshot's archive. glesha copies the object inside AWS with the new class. The archive identity and older object versions are kept. If the object is in cold storage, it may need a retrieval first.

`storage-class` works only with AWS copies and does not support chunked snapshots. The accepted classes are listed in [Storage classes](configuration.md#storage-classes).

| Option | Description |
| --- | --- |
| `--class CLASS` | New storage class. Required. |
| `--snapshot ID\|latest` | Snapshot to change. Default: `latest`. |
| `--from REMOTE...` | Buckets that the command may access. |
| `--to REMOTE...` | Other buckets that the command may access. |
| `--catalog-passphrase-file FILE\|-` | Catalog passphrase. |

```sh
glesha storage-class docs --snapshot SNAPSHOT_ID --class DEEP_ARCHIVE
```

### check

```text
glesha check --to REMOTE... [options]
```

Tests each remote with a temporary object that glesha creates. For each remote, glesha reports these steps: `Safety`, `Write`, `Metadata`, `Read`, and `Cleanup`. Each step shows `OK`, `FAILED` with the error, or `SKIPPED`. A failure on one remote does not stop the checks of the others.

glesha does not write to a bucket that has a default Object Lock retention. It never replaces or deletes existing objects. If the cleanup step fails, glesha prints the key and version of the object that remains.

```text
Provider "b2" (b2)
  Safety     OK
  Write      OK
  Metadata   OK
  Read       OK
  Cleanup    OK
  Result      PASS

Summary: 1 passed, 0 failed.
```

| Option | Description |
| --- | --- |
| `--to REMOTE...` | Remotes to test. |
| `--from REMOTE...` | Other buckets that the command may access. |

Also accepts the [performance options](#performance-options).

```sh
glesha check --to aws,b2
```

### state

```text
glesha state migrate [options]
```

Moves state from the location used before version 0.5.2, `glesha/state-v2` in the configuration directory, to the current [state directory](configuration.md#local-state). Stop other glesha processes first.

The migration makes no cloud requests. It moves archive files without copying or re-encrypting them, so both locations must be on the same file system. It keeps copies of the original databases in `.state-migration-backup` in the new directory. Configuration and credential files are not touched.

The migration fails if the destination already exists. To migrate into another directory, pass a new path with `--state-dir`. If the migration stops, run the same command again.

```sh
glesha state migrate
```

### archive

```text
glesha archive PATH... [options]
```

Creates an encrypted archive of one or more paths. No set and no remote are needed. glesha asks for the passphrase twice.

Without `--output`, the archive is written to the current directory as `glesha-TIMESTAMP.tar.xz.gpg`. The extension follows the compression format.

| Option | Description |
| --- | --- |
| `--output FILE` | Output file. It must not exist. |
| `--compression FORMAT` | `xz`, `gzip`, or `bzip2`. Default: `xz`. |
| `--compression-level LEVEL` | `1` to `9`. Default: `6`. |
| `--archive-mode MODE` | `auto`, `memory`, or `stream`. |
| `--prefix PREFIX` | `glesha` or `encb`. |
| `--storage-class CLASS` | Accepted and ignored. |
| `--passphrase-file FILE\|-` | Archive passphrase. |

Also accepts the [performance options](#performance-options).

```sh
glesha archive ~/Documents
glesha archive ~/Documents ~/Pictures --compression gzip --output personal.tar.gz.gpg
```

### decrypt

```text
glesha decrypt FILE [options]
```

Decrypts an archive, checks it, and writes the compressed tar stream to a new file. Without `--output`, the output file is in the same directory as `FILE`. Its name is the archive name with the extension of the compression format, for example `.tar.xz`. Use `extract` to unpack the files instead.

| Option | Description |
| --- | --- |
| `--output FILE` | Output file. It must not exist. |
| `--passphrase-file FILE\|-` | Archive passphrase. |
| `--memory-max SIZE` | Memory budget. See [Performance options](#performance-options). |

```sh
glesha decrypt personal.tar.xz.gpg
glesha decrypt old.tar.gz.gpg --output old.tar.gz --passphrase-file ~/secrets/archive
```

### extract

```text
glesha extract FILE --into DIR [options]
```

Checks an archive and extracts it into `DIR`. `FILE` can be an encrypted archive or an unencrypted compressed tar file. `DIR` must not exist.

| Option | Description |
| --- | --- |
| `--into DIR` | New destination directory. Required. |
| `--passphrase-file FILE\|-` | Archive passphrase. |
| `--memory-max SIZE` | Memory budget. See [Performance options](#performance-options). |

```sh
glesha extract personal.tar.xz.gpg --into ~/recovered
glesha extract personal.tar.xz --into ~/recovered
```

### upload

```text
glesha upload FILE --to REMOTE... [options]
```

Uploads a file to each remote in `--to` in parallel. No set is involved. glesha checks every remote before it uploads. If the key exists on any of them, the upload fails unless you pass `--force`.

| Option | Description |
| --- | --- |
| `--to REMOTE...` | Destination remotes. |
| `--key KEY` | Object key. Default: the file name. |
| `--storage-class CLASS` | AWS storage class. |
| `--force` | Replace an existing object. |
| `--from REMOTE...` | Other buckets that the command may access. |

Also accepts the [performance options](#performance-options).

```sh
glesha upload personal.tar.xz.gpg --to b2
glesha upload personal.tar.xz.gpg --to aws,b2 --key backups/personal.tar.xz.gpg
```

### download

```text
glesha download KEY --from REMOTE... [options]
```

Downloads the object `KEY`. glesha tries each remote in `--from` in order until one succeeds. `--from` is required. Without `--output`, the file is written to the current directory with the last component of `KEY` as its name.

| Option | Description |
| --- | --- |
| `--from REMOTE...` | Remotes to try, in order. |
| `--output FILE` | Output file. It must not exist. |
| `--to REMOTE...` | Other buckets that the command may access. |

Also accepts the [performance options](#performance-options).

```sh
glesha download backups/personal.tar.xz.gpg --from b2,aws
glesha download backups/personal.tar.xz.gpg --from b2 --output personal.tar.xz.gpg
```

### help

```text
glesha help [COMMAND [ACTION]]
```

Prints the options and examples of a command. For `catalog`, an action can follow, for example `glesha help catalog unlock`. `help` does not read the configuration file or credentials.

```sh
glesha help
glesha help run
glesha help catalog sync
```

### version

```text
glesha version
glesha --version
glesha -v
```

Prints the version and the build revision, for example `glesha 0.5.2 (1a2b3c4)`. A development build prints `glesha dev (unknown)`. `version` does not read the configuration file or credentials, and it accepts no arguments.

## Output

glesha writes results to standard output. Progress, prompts, warnings, and errors go to standard error. You can redirect standard output to a file and still see progress.

Human output uses binary units with two decimal places, for example `14.20M`. Times are shown in the local time zone.

### Progress

With `--progress auto` and a terminal, glesha prints `[+]` before each step and draws a progress bar for long steps:

```text
[63.35%] [1.27G/2.34G] [1.22M/s]
```

When standard error is not a terminal, or `TERM=dumb`, glesha prints one line per step without a bar. `--progress plain` selects this form on a terminal. `--progress off` and log levels `warn`, `error`, and `silent` turn progress off.

<h3 id="structured-output">Structured output</h3>

With `--format json`, every command writes JSON Lines to standard output. Each line is one record:

```json
{"version":2,"kind":"set","data":{"id":"87156a05-ea28-45fe-81df-bb74a4a50f3a","name":"docs"}}
```

| Field | Description |
| --- | --- |
| `version` | Version of the record format for this kind. |
| `kind` | Type of record. |
| `data` | The record. Byte counts are integers, prices are numbers, and times are in UTC. |

A command that lists items writes one record per item. An empty list writes no records. Records of kind `catalog_state`, `catalog_sync_summary`, and `catalog_sync_error` use version 3. All other result records use version 2.

These are the result kinds:

| Kind | Written by |
| --- | --- |
| `set` | `create`, `configure`, `list` |
| `snapshot` | `run`, `history`, `import` |
| `no_changes` | `run` when no file changed |
| `entry` | `browse` |
| `restore`, `restore_cancelled` | `restore` |
| `status` | `status` |
| `catalog_state`, `catalog_sync_summary`, `catalog_sync_error` | `catalog sync` |
| `catalog_unlocked` | `catalog unlock` |
| `resolution_difference`, `resolution_choices` | `catalog resolve` |
| `cleanup_candidate`, `cleanup_removed`, `cleanup_summary` | `cleanup` |
| `provider_check`, `check_summary` | `check` |
| `download` | `download` |
| `remote_set` | `list --remote` |
| `result` | Other commands that report one outcome |
| `help` | `help` |
| `version` | `version` |

With `--format json`, standard error also carries JSON Lines, with version 1 records. `kind` is `progress`, `error`, `warning`, `info`, or `debug`. Progress records appear when a step starts and stops, and once per second while it runs. They have these fields:

| Field | Description |
| --- | --- |
| `id` | Step identifier, unique within one command. |
| `phase` | Name of the step. |
| `state` | State of the step. `STOPPED` means that the step ended, not that it succeeded. |
| `completed_bytes` | Bytes processed so far. |
| `total_bytes` | Total bytes, or `null` if unknown. |
| `bytes_per_second` | Current rate. |
| `timestamp` | UTC time of the record. |

Error records have the fields `code`, `message`, and `exit_code`. glesha writes them even with `--log-level silent`.

| Code | Meaning |
| --- | --- |
| `FAILED` | The operation failed. |
| `INVALID_ARGUMENT` | The command line is invalid. |
| `PENDING` | Work is pending, for example a cold-storage retrieval. |
| `CONFLICT` | The remote history conflicts with the local history. |
| `CONFIRMATION_REQUIRED` | The command needs a confirmation that it did not get. |
| `CANCELED` | The command was interrupted. |

`--format json` does not answer prompts. For unattended use, pass passphrase files and `--assume-yes` or `--request-retrieval` where needed.

## Set states

`status` reports one of these states for each set:

| State | Meaning |
| --- | --- |
| `NEW` | The set has no snapshots yet. |
| `SYNCED` | Local records show no outstanding work. This is not a live check of the remote copies. |
| `RUNNING` | Another glesha process is working on the set. |
| `PAUSED` | An operation was interrupted. Run `glesha retry SET`, or repeat the interrupted `restore` or `import` command. |
| `LOCAL` | The local catalog has changes that are not published. Run `glesha catalog sync SET`. |
| `PUBLISHING` | Another glesha process is publishing the catalog of the set. |
| `WAITING` | A restore or import is waiting for a cold-storage retrieval. Repeat the command when the archives are readable. |
| `STALE` | Local state is out of date. Registry copies need repair, the catalog is missing locally, or downloaded changes are not unlocked yet. The details give the command to run. |
| `CONFLICT` | The local and remote histories conflict. Run `glesha catalog resolve SET`. |
| `FAILED` | The last operation failed. The details show the error. |
| `LOCKED` | The catalog needs its passphrase. Run `glesha catalog unlock SET`. |

## Exit status

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | The operation failed. |
| `2` | The command line is invalid, or a named set or snapshot does not exist. |
| `3` | Work is pending, for example a cold-storage retrieval. Run the command again later. |
| `4` | The remote history conflicts with the local history. |
| `130` | The command was interrupted, for example with Ctrl+C. |

## Compatibility

Archive, catalog, and JSON output formats are versioned. Older archives, including `encb` archives, stay readable. [compatibility.md](../compatibility.md) describes the policy.
