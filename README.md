# glesha

A standalone Go executable for encrypted archives and managed backups to
Backblaze B2 and AWS S3. Tar, xz, gzip, bzip2, OpenPGP, SQLite and transfers run
internally. GPG and tar can recover full archives independently of glesha.

This rewrite uses fresh application state. It neither migrates nor deletes old
catalogs. Existing encb `.tar.gz.gpg` archives remain unchanged and readable.
The user guide, complete options and examples are in [cli_reference.md](cli_reference.md).
Use `glesha version`, `glesha -v`, `glesha -version` or `glesha --version` to
show the executable version and build revision without loading credentials.
Use `glesha help COMMAND` for quick help; manual sources are in [man/](man/).
Persistent-format and CLI evolution follow [compatibility.md](compatibility.md).

## Build and test

Go 1.25 or newer:

```
CGO_ENABLED=0 go build -o build/glesha .
./build/glesha help
go test ./...
go test -race ./...
```

During development use `./build/glesha`: plain `glesha` may still resolve to an
older installed executable on your PATH. Building does not replace that executable.

The tests use temporary local files, in-memory providers and HTTP mocks. Optional
interoperability tests invoke GPG/tar; production code starts no subprocesses.
Yesb is reserved for release preparation after feature completion. No development
build requires Docker, Python, signing tools, or Yesb.

Colors default to `--color=auto`; `yes` forces them and `no` disables them.
Interactive stderr marks steps with `[+]` and shows throttled byte progress
with two decimal places and average transfer speed. Use `--log-level=debug` for
operation diagnostics. Use `--jobs N` or `-j N` for up to N concurrent transfer workers;
`--hash-workers N` controls hashing separately. Explicit worker flags show the
effective worker count for scanning, archiving and transfers. JSON and redirected stderr have no progress animation.

Test provider access explicitly with `glesha check --to b2 --env /secure/cloud.env`.
The summary reports write, metadata, verified read and cleanup for each provider.
Use `--json` for automation. Checks create an owned temporary object; `status`
shows ASCII tables of catalog availability, stored bytes, last upload and estimated
annual storage cost. `status SET` adds details; `--refresh` measures live metadata
without pulling catalogs or initiating cold retrieval. Estimates use dated, offline
[provider pricing](pricing/README.md) and mark unknown amounts as partial.

## Register, back up, and recover

```
glesha create docs ~/Documents --to b2
glesha run docs --env /path/to/credentials
glesha run docs --incremental --env /path/to/credentials
glesha history docs
glesha browse docs --path Documents
glesha restore docs --into ~/recovered --env /path/to/credentials
```

`create` saves sources and settings locally. It creates no archive and makes no
cloud call. `run` creates a full backup unless `--incremental` is supplied. There
is no destination default: supply `--to` at creation or run time. A set has a
stable UUID; its name is a convenient local handle. Snapshot UUIDs identify
individual recovery points. Saved source directories cannot be replaced under
the same set name.

Use `--to aws,b2` or `--to aws --to b2` for independent full copies. Incremental
sets require every historical archive on one authoritative provider. Per-run
provider switching is rejected once incremental authority is established.
`move docs --to other-remote` copies the complete history before switching and
retains source copies. Additional remote profiles use `[remotes.NAME]` in TOML;
current adapters support AWS and B2.

Successful runs remove automatically named local archives after uploads and catalog
publication finish. Use `run docs --keep-archive` to retain one; explicit `--output`
files and standalone `archive` outputs are always retained.

Interrupted runs retain completed encrypted archives and recorded uploads.
Repeat `run docs` or use `retry docs`. Completed stages are reused; interrupted
scans, encryption, downloads, or multipart uploads can restart. Multipart parts
are not persistently resumed. If the completed archive was relocated, use
`retry docs --archive /new/path/archive.gpg`.

Missing paths and type replacements in incrementals require a default-no `[y/N]`
confirmation. `--assume-yes` approves these changes for automation. Historical
snapshots remain available. Output files/directories must be new; restore and
extraction never replace an existing destination. Raw uploads require `--force`
to replace an existing remote key.

## Standalone archives and legacy imports

```
glesha archive ~/Documents --passphrase-file /secure/archive-password
glesha archive ~/Documents --compression gzip --prefix encb
glesha decrypt archive.tar.xz.gpg --passphrase-file /secure/archive-password
glesha extract archive.tar.xz.gpg --into recovered --passphrase-file /secure/archive-password

glesha import old-docs --file encb-old.tar.gz.gpg
glesha import old-docs --from b2 --key backups/encb-old.tar.gz.gpg --env /path/to/credentials
glesha configure old-docs --root Documents=~/Documents --to b2 --catalog-to b2
glesha catalog push old-docs --env /path/to/credentials
```

Archives default to xz level 6. Gzip and bzip2 are selectable; detection uses decrypted
signatures, independent of filenames. Each tar has a filename-stem wrapper.
Managed restores remove wrappers and apply deltas/deletions. Full archives can
also be recovered with `gpg --decrypt` followed by `tar -xJf`, `tar -xjf` or `tar -xzf`.
Incremental archives require their baseline and relevant deltas.

XZ uses the pinned pure-Go `github.com/ulikunitz/xz` encoder with CRC64 checks.
Glesha levels 1..9 select dictionaries from 64 KiB to 16 MiB, doubling each level;
level 6 uses 2 MiB. These are not the external `xz` command's presets. Higher
levels need larger memory budgets. Imported xz streams use LZMA2 without extra
filters; oversized dictionaries fail before allocation. Existing sets retain
their saved compression; use `configure SET --compression xz` to switch.

Import downloads a remote archive once, validates and inventories it, and records
its exact remote identity. It uploads no payload and does not recompress or
re-encrypt the original archive. Repeat imports identify matching encrypted bytes
by checksum. Unknown original backup dates remain unknown; `--date RFC3339`
records a supplied date. `--baseline new` explicitly chooses a new import as the
current baseline; `configure SET --baseline SNAPSHOT_ID` selects an existing one.
Source mappings are optional for browsing/restoring imports and required before
running new backups.

## Catalogs and discovery

```
glesha list
glesha list --remote --from b2 --env /path/to/credentials
glesha catalog pull docs --env /path/to/credentials
glesha catalog push docs --env /path/to/credentials
glesha browse docs --recursive --json
```

`registry.db` stores sets and remote profiles. Each set UUID has a `catalog.db`
using SQLite 3. Catalogs store metadata and binary hashes, never file contents.
Parent/name nodes, immutable metadata versions, and membership intervals reuse
unchanged entries across snapshots. Deletion preserves history; node IDs are not
reused. Paths, totals and counts are derived locally. Size depends on names,
metadata and changes; there is no fixed 100 MB guarantee for a million files.
The included synthetic million-file benchmark measures roughly 176 MiB locally
and 53 MiB as a gzip checkpoint; real trees and retained changes vary.

Browsing reads only the local catalog. Remote discovery registers set descriptors
so a fresh machine can pull without remembering set names. Metadata publication
uses compressed change batches, periodic checkpoints and immutable revisions.
Deltas include only changed tree, snapshot and storage records. AWS conditionally
commits a revision after its batch and descriptor are complete; unfinished
proposals are invisible and do not block later publication.
Remote catalogs are encrypted by default and always stored in S3 Standard.
Cached revision heads avoid repeatedly downloading old metadata. A pull preserves
an existing local catalog before replacing it. Conflicting histories are retained
and reported instead of silently overwritten. Use `catalog reconcile SET` for a
pending branch whose remote parent advanced; `catalog abandon SET` preserves
pending work in an abandoned database. Concurrent remote writers are unsupported.

## AWS archive tiers

```
glesha storage-class docs --snapshot ID --class DEEP_ARCHIVE
glesha status docs --refresh --env /path/to/credentials
glesha restore docs --into recovered --request-retrieval --env /path/to/credentials
```

Class changes use server-side copy and retain archive identity, initial class and
older versions. Lifecycle policies may change classes outside glesha. `--refresh`
observes the provider's current class/readiness; recovery also checks the actual
object before requesting retrieval. Stored class values are observations, not a
promise that an object remains readable.

Readable registered copies take precedence over charged retrieval. If needed,
glesha asks for retrieval consent and records a pinned recovery job. Bulk is the
cheapest default. Use `--retrieval standard` or `--retrieval expedited` explicitly
for faster tiers; unsupported combinations fail. Deep Archive supports Bulk and
Standard, typically up to 48 and 12 hours respectively. Repeat the recovery
command after AWS makes objects readable. AWS maintains a temporary readable
copy for seven days by default; the permanent Deep Archive object stays there.
`--request-retrieval` authorizes charges in automation. `--assume-yes` does not.

## Configuration and credentials

Nonsecret defaults live in TOML. See [config-sample.toml](config-sample.toml).
The default path is the platform user-config directory under `glesha/config.toml`.
CLI options override TOML, which overrides built-in defaults.

Credentials come only from process environment or an explicitly supplied `--env`
file. Existing variables take precedence. Sensitive files are never rewritten.
AWS uses `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` (encb names
`AWS_ACCESS_KEY`/`AWS_SECRET_KEY` are accepted), optionally `AWS_SESSION_TOKEN`.
B2 uses `B2_KEY_ID`/`B2_APPLICATION_KEY`. Bucket environment names remain
`AWS_BUCKET_NAME`/`B2_BUCKET_NAME`, with `B2_BUCKET_ENDPOINT` for B2.

Archive passwords use hidden prompts, confirmed at creation, or
`--passphrase-file FILE|-`. Catalog encryption uses a separate
`GLESHA_CATALOG_PASSPHRASE` or `--catalog-passphrase-file FILE|-`. No password is
stored. Restore accepts repeated `--archive-passphrase-file SNAPSHOT_ID=FILE`
overrides for archives with different passwords.

`memory.max` is the operating budget; a configured value never queries available
system memory. Otherwise half available memory is used, with a conservative
128 MiB fallback. Workers, buffers, SQLite caches and Go's soft memory limit share
the managed budget. This is not an absolute RSS limit. Auto mode spills to disk;
explicit memory mode fails if the archive cannot fit. Filesystem inventories and
historical trees are paged through SQLite rather than retained in RAM.

ACL/xattr fidelity, automatic retention, graphical browsing and persistent
multipart resume are outside this version. Linux is the primary tested platform;
macOS and Windows builds are also checked.

## Direct release bundles

The initial distribution is Linux amd64 and arm64 tar.gz bundles. Each contains
`bin/glesha`, `share/man/man1/`, `install.txt`, LICENSE and a sample TOML file.
Verify the adjacent `.sha256` file, extract into a new staging directory, then
follow `install.txt` to install under `~/.local` or `/usr/local`. Upgrades replace
only the binary and matching manpages; configuration and backup state stay local.

Run `./yesb/build_all.py` for release preparation. Project-owned packaging adapts
Yesb's archive layout without changing the toolkit. Only the two tar.gz bundles
are declared as direct R2 release artifacts under `glesha/releases`; no APT/RPM
repository or curl installer is configured. The public download host is pending.
Do not run a publisher until the destination, version and artifacts are approved.

## Chunked backups

Use `glesha run docs --chunked --spool-max 128M` when a complete local archive
would not fit. Each independently encrypted chunk is uploaded before its local
spool is removed. `128M` / `1.1G` are decimal; MiB / GiB are binary. Repeat `run`
with the original passphrase to resume. Sources are re-read and checked; completed
chunks are reused. See [the CLI reference](cli_reference.md#bounded-disk-space)
for recovery and current command restrictions.
