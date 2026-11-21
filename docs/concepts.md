# Concepts

## Backup sets

A backup set is a named list of source directories and the remotes that store their backups.

Each set has two identifiers:

- A name, such as `docs`, that you choose.
- A UUID, such as `87156a05-ea28-45fe-81df-bb74a4a50f3a`, that glesha assigns. The UUID never changes.

Commands accept either identifier as `SET`. The name is stored in the encrypted catalog, so on a new computer a set appears by UUID until you unlock the catalog.

Each source directory becomes a root in the set. The root name is the last part of the path, so `~/Documents` becomes `Documents`. Paths in the catalog start with the root name, for example `Documents/notes/a.txt`.

A binding links a root name to a directory on one computer. Bindings are not published with the catalog. On a new computer, bind each root with `glesha configure SET --root NAME=PATH` before you make new backups. Browse and restore do not need bindings.

## Snapshots

A snapshot is one backup of a set at one point in time. Each `glesha run` creates at most one snapshot. Each snapshot has a UUID.

A snapshot is one of two kinds:

- A full snapshot contains every file in the set's roots.
- An incremental snapshot contains only the files that changed since an earlier snapshot, called its baseline.

`run` makes a full snapshot unless you pass `--incremental`.

A restore of an incremental snapshot needs every archive back to the last full snapshot. For this reason, the complete history of a set must be on one remote. `glesha move` copies the complete history before it changes the destination.

Before glesha records a removed file, or a path that changed type, it asks for confirmation. Older snapshots keep the old files.

## Archives

Each snapshot is stored as an archive. An archive is a tar stream, compressed with xz, gzip, or bzip2, and encrypted as a binary OpenPGP message.

Standard tools can open the archive of a full snapshot:

```sh
gpg --decrypt documents.tar.xz.gpg | tar -xJf -
```

The archive contains one top-level directory named after the archive. Inside it are the set's roots and a `.glesha-manifest-v1.json` file that describes the archive.

A chunked snapshot is stored as many encrypted chunks, each with part of the tar stream. Only glesha can restore a chunked snapshot. See [Back up with limited disk space](guides.md#bounded-disk-space).

glesha detects the compression format from the archive contents, not the file name. Archives with the older `encb` prefix are readable without conversion, and `glesha import` adds them to a set.

## Catalogs

The catalog is a database that records, for each snapshot of one set, the files, their sizes and hashes, and the location of each archive. `history`, `browse`, and `status` read the catalog and download no archive data.

glesha keeps two copies of each catalog:

- A local SQLite database in the [state directory](configuration.md#local-state).
- A copy in a remote, called the catalog location. The default is the first remote in `--to`. `--catalog-to local` keeps the catalog only on your computer.

The catalog passphrase encrypts the remote copy. Object keys contain only UUIDs, so set names and file names are not visible in the bucket.

Commands that change a set publish the catalog when they finish. `glesha catalog sync` downloads and merges changes from other computers. If two computers make incompatible changes, sync stops with a conflict, and `glesha catalog resolve` selects the side to keep. Two computers must not write to the same set at the same time.

## Passphrases

glesha uses two passphrases:

- The archive passphrase encrypts archive contents. You need it to restore files.
- The catalog passphrase encrypts the remote catalog. You need it to read the list of files on a new computer.

The catalog passphrase does not decrypt archives. glesha stores neither passphrase. Without the archive passphrase, the archives cannot be decrypted.

## Remotes

A remote is a named bucket. glesha supports two kinds:

- `aws`: Amazon S3.
- `b2`: Backblaze B2, through its S3-compatible API.

The remotes `aws` and `b2` always exist. Define more in the [configuration file](configuration.md#remotesname). All remotes of one kind use the same credentials.

With `--to aws,b2`, glesha uploads a full snapshot to both remotes in parallel. An incremental snapshot goes to exactly one remote, and the first incremental snapshot binds the set to that remote. For reads, glesha tries remotes in the order of `--from`.

With `--from` or `--to`, glesha accesses only the named buckets. A command that needs another bucket fails before it contacts that bucket.

## Storage classes and cold storage

Each AWS object has a storage class. Objects in `GLACIER` and `DEEP_ARCHIVE` cost less to store but are not directly readable. AWS must first create a temporary readable copy, called a retrieval. Retrievals are charged and can take hours.

glesha requests a retrieval only after you confirm the prompt or pass `--request-retrieval`. The default tier is Bulk, the slowest and least expensive. The readable copy lasts three days by default.

While a retrieval is pending, `restore` exits with status 3. Run the same command again when the archives are ready.

B2 has no storage classes. All B2 objects are directly readable.

## Local state

The state directory holds the registry of sets, one catalog database per set, unfinished operations, and some archive copies.

glesha records each step of a long operation. If an operation stops, run the same command again or run `glesha retry SET`. Finished archives and uploads are reused. A step that was in progress, such as a multipart upload, can restart.

## Safety rules

These rules apply to every command:

- glesha never overwrites a local file. Output files and extract destinations must not exist. Restore merges into an existing directory but stops if any file it would write already exists.
- glesha never deletes a remote backup. `cleanup` removes only local copies, and only after it confirms the remote copy. `move` keeps the source copies.
- glesha replaces a remote object only when you pass `upload --force`.
- glesha asks before it records deletions, removes local files, or resolves a conflict. `--assume-yes` answers yes for one command.
- glesha does not convert existing archives or catalogs without an explicit migration.
