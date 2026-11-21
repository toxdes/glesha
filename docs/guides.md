# How-to guides

Each guide solves one task. The examples omit `--env FILE`. Add it if your credentials are in a file. See [Credentials](configuration.md#credentials).

**Back up**

- [Back up several directories as one set](#back-up-several-directories-as-one-set)
- [Schedule nightly backups](#schedule-nightly-backups)
- [Keep copies on two remotes](#keep-copies-on-two-remotes)
- [Store long-term backups in AWS cold storage](#store-long-term-backups-in-aws-cold-storage)
- [Back up with limited disk space](#bounded-disk-space)
- [Use another S3-compatible service](#use-another-s3-compatible-service)

**Find and restore**

- [Find a file in your backups](#find-a-file-in-your-backups)
- [Restore one file](#restore-one-file)
- [Restore an older snapshot](#restore-an-older-snapshot)
- [Restore from AWS cold storage](#restore-from-aws-cold-storage)
- [Recover on a new computer](#recover-on-a-new-computer)

**Maintain**

- [Check backup state and cost](#check-backup-state-and-cost)
- [Resume an interrupted backup](#resume-an-interrupted-backup)
- [Change the storage class of a snapshot](#change-the-storage-class-of-a-snapshot)
- [Move a set to another remote](#move-a-set-to-another-remote)
- [Free local disk space](#free-local-disk-space)
- [Resolve a catalog conflict](#resolve-a-catalog-conflict)
- [Move state from an older version](#move-state-from-an-older-version)
- [Test glesha with separate state](#test-glesha-with-separate-state)

**Work with archives**

- [Encrypt a directory into one file](#encrypt-a-directory-into-one-file)
- [Open an archive without glesha](#open-an-archive-without-glesha)
- [Import an existing archive](#import-an-existing-archive)

## Back up

### Back up several directories as one set

Pass every directory to `create`:

```sh
glesha create home ~/Documents ~/Pictures ~/Projects --to b2
glesha run home
```

Each directory becomes a root, named after the last part of its path. `glesha browse home` lists `Documents`, `Pictures`, and `Projects` at the top level.

Two directories with the same name, such as `~/a/Docs` and `~/b/Docs`, cannot be in one set. `create` fails with `colliding basename`. Put them in separate sets.

### Schedule nightly backups

A scheduled job has no terminal, so it must receive every passphrase and confirmation on the command line.

1. Store each passphrase in its own file, and restrict access to the files:

	```sh
	chmod 600 ~/.config/glesha/archive-passphrase ~/.config/glesha/catalog-passphrase
	```

2. Run the backup once by hand with the same options that the job uses:

	```sh
	glesha run docs --incremental \
		--env ~/.config/glesha/cloud.env \
		--passphrase-file ~/.config/glesha/archive-passphrase \
		--catalog-passphrase-file ~/.config/glesha/catalog-passphrase \
		--assume-yes \
		--progress off
	```

	`--assume-yes` records removed files without a prompt. Without it, the job fails when a file is removed.

3. Add the job to your crontab with `crontab -e`. This entry runs at 02:30 every night:

	```text
	30 2 * * * $HOME/.local/bin/glesha run docs --incremental --env $HOME/.config/glesha/cloud.env --passphrase-file $HOME/.config/glesha/archive-passphrase --catalog-passphrase-file $HOME/.config/glesha/catalog-passphrase --assume-yes --progress off
	```

On systems with systemd, use a user timer instead. Create `~/.config/systemd/user/glesha-docs.service`:

```ini
[Unit]
Description=glesha backup of docs

[Service]
Type=oneshot
ExecStart=%h/.local/bin/glesha run docs --incremental --env %h/.config/glesha/cloud.env --passphrase-file %h/.config/glesha/archive-passphrase --catalog-passphrase-file %h/.config/glesha/catalog-passphrase --assume-yes --progress off
```

Create `~/.config/systemd/user/glesha-docs.timer`:

```ini
[Unit]
Description=Nightly glesha backup of docs

[Timer]
OnCalendar=*-*-* 02:30:00
Persistent=true

[Install]
WantedBy=timers.target
```

Enable the timer:

```sh
systemctl --user daemon-reload
systemctl --user enable --now glesha-docs.timer
```

The exit status reports the result: `0` for success. [Exit status](cli-reference.md#exit-status) lists the other values. For machine-readable results, add `--format json`.

An incremental chain grows with each run. To start a new chain, schedule `glesha run docs --full` at a longer interval, for example once a month.

### Keep copies on two remotes

An incremental snapshot can go to only one remote. Choose a method by the kind of backup.

To keep incremental backups on two remotes, create one set for each remote. Both sets can use the same directory:

```sh
glesha create docs-aws ~/Documents --to aws
glesha create docs-b2 ~/Documents --to b2
glesha run docs-aws --incremental
glesha run docs-b2 --incremental
```

Each set has its own history, catalog, and schedule.

To upload full snapshots to two remotes in one run, give both remotes to one set:

```sh
glesha create docs ~/Documents --to aws,b2
glesha run docs
```

glesha uploads the snapshot to both remotes in parallel. The catalog is stored on the first remote unless you set `--catalog-to`. `run --incremental` fails for this set unless you pass a single remote, for example `--to b2`. After the first incremental snapshot, the set uses only that remote. To switch it to another remote later, see [Move a set to another remote](#move-a-set-to-another-remote).

### Store long-term backups in AWS cold storage

Use this setup for data that you rarely restore. Storage in `DEEP_ARCHIVE` costs less than `STANDARD`, but each restore requires a retrieval, which is charged and takes up to 48 hours.

1. Create the set with cold storage classes:

	```sh
	glesha create archive-2025 ~/Archive/2025 --to aws \
		--storage-class DEEP_ARCHIVE \
		--incremental-storage-class DEEP_ARCHIVE
	```

2. Run the backup:

	```sh
	glesha run archive-2025
	```

3. Check the class of each copy:

	```sh
	glesha history archive-2025
	```

	The `COPIES` column shows `retrieval required (DEEP_ARCHIVE)`.

`history` and `browse` read the local catalog, so you can list files without a retrieval. To restore, see [Restore from AWS cold storage](#restore-from-aws-cold-storage).

To keep recent snapshots readable and move older ones to cold storage, keep the set on `STANDARD` and change old snapshots with [`storage-class`](#change-the-storage-class-of-a-snapshot).

<h3 id="bounded-disk-space">Back up with limited disk space</h3>

A normal backup writes the complete encrypted archive to local disk before the upload. If the disk does not have space for the archive, use a chunked backup. glesha uploads encrypted chunks and keeps at most `--spool-max` of chunk data on disk.

For a new set:

```sh
glesha create media ~/Videos --to b2 --chunked --spool-max 256MiB
```

For an existing set:

```sh
glesha configure media --chunked --spool-max 256MiB
```

For one run only:

```sh
glesha run media --chunked --spool-max 128M
glesha run media --chunked=false
```

`--spool-max` defaults to 128 MiB, with a minimum of 2 MiB. `M` and `G` are decimal units. `MiB` and `GiB` are binary units.

Chunked backups have these limits:

- `run` does not accept `--output`, `--keep-archive`, or `--archive-mode memory`.
- `move` and `storage-class` do not support chunked snapshots.
- A snapshot can have at most 65,536 chunks.
- Only glesha can restore a chunked snapshot.

### Use another S3-compatible service

Define a remote of kind `aws` with the endpoint of the service:

```toml
[remotes.minio]
kind = "aws"
bucket = "backups"
region = "us-east-1"
endpoint = "https://minio.example.com"
```

Set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` to the credentials for the service. Test access, then use the remote by name:

```sh
glesha check --to minio
glesha create docs ~/Documents --to minio
```

glesha sends path-style requests to a custom endpoint. Every `aws` remote uses the same `AWS_*` credentials, so one command cannot use Amazon S3 and another service with different credentials.

## Find and restore

### Find a file in your backups

To search the latest snapshot, filter the file list:

```sh
glesha browse docs --recursive | grep report.pdf
```

To list every snapshot that contains a file, combine `history` and `browse` with `jq`:

```sh
glesha history docs --format json | jq -r '.data.snapshot.id' |
while read -r id; do
	glesha browse docs --snapshot "$id" --path Documents --format json |
		jq -r --arg id "$id" 'select(.data.entry.path == "Documents/report.pdf") | "\($id) \(.data.entry.size)"'
done
```

Each output line has a snapshot ID and the file size in bytes in that snapshot. These commands read only the local catalog.

### Restore one file

`restore` writes a complete snapshot. To recover one file, restore into a temporary directory and copy the file:

```sh
glesha restore docs --snapshot SNAPSHOT_ID --into /tmp/glesha-restore
cp /tmp/glesha-restore/Documents/report.pdf ~/Documents/
rm -rf /tmp/glesha-restore
```

To find the right `SNAPSHOT_ID`, see [Find a file in your backups](#find-a-file-in-your-backups).

### Restore an older snapshot

1. Find the snapshot ID:

	```sh
	glesha history docs
	```

2. Optional: list its files:

	```sh
	glesha browse docs --snapshot SNAPSHOT_ID --recursive
	```

3. Restore it:

	```sh
	glesha restore docs --snapshot SNAPSHOT_ID --into ~/recovered
	```

To select remotes and their order, add `--from b2,aws`.

If snapshots in the chain use different archive passphrases, give each one a file:

```sh
glesha restore docs --into ~/recovered \
	--passphrase-file ~/secrets/archive-new \
	--archive-passphrase-file SNAPSHOT_ID=~/secrets/archive-old
```

If a restore stops, run the same command again. To discard an unfinished restore, run `glesha restore docs --cancel`. Restored files are kept.

### Restore from AWS cold storage

Archives in `GLACIER`, `DEEP_ARCHIVE`, or an archive tier of `INTELLIGENT_TIERING` require a retrieval. Retrievals are charged. Bulk retrievals from `DEEP_ARCHIVE` take up to 48 hours.

1. Start the restore and allow the retrieval:

	```sh
	glesha restore docs --into ~/recovered --request-retrieval
	```

	glesha requests the retrieval and exits with status 3.

	For a faster, more expensive tier, add `--retrieval standard` or `--retrieval expedited`. To keep the readable copy longer than three days, add `--days N`.

2. Check progress:

	```sh
	glesha status --refresh
	```

	The Retrievals table shows the number of archives that are `READY`.

3. When all archives are ready, run the restore command again:

	```sh
	glesha restore docs --into ~/recovered --request-retrieval
	```

### Recover on a new computer

You need the bucket configuration, the catalog passphrase, and the archive passphrase.

1. Install glesha and configure the remotes.
2. Download the catalogs:

	```sh
	glesha catalog sync
	```

	Encrypted catalogs appear by UUID:

	```text
	SET                                   RESULT
	87156a05-ea28-45fe-81df-bb74a4a50f3a  Updated (locked)
	Catalog sync complete. 1 updated; 1 locked.
	```

3. Unlock the catalog:

	```sh
	glesha catalog unlock 87156a05-ea28-45fe-81df-bb74a4a50f3a
	```

	The set has its original name again. `glesha list` shows it.

4. Restore the files:

	```sh
	glesha restore docs --into ~/recovered
	```

To continue backups from this computer, bind each root to a local directory. `glesha browse docs` lists the root names:

```sh
glesha configure docs --root Documents=~/Documents
glesha run docs --incremental
```

## Maintain

### Check backup state and cost

```sh
glesha status
```

`SYNCED` means that the set has no unfinished work. Any other state is listed under **Needs attention** with the next command to run. [Set states](cli-reference.md#set-states) defines each state.

For details on one set, including the last backup time, destinations, and the storage classes in use:

```sh
glesha status docs
```

To confirm that the recorded archives still exist in the buckets:

```sh
glesha status --refresh
```

`EST/YR` estimates the yearly storage cost. It excludes request, retrieval, and egress fees. For a spreadsheet, use `glesha status --format csv`.

### Resume an interrupted backup

Run the same command again with the same archive passphrase:

```sh
glesha run docs
```

glesha prints `Resuming previous run...` and reuses finished archives and uploads.

`retry` resumes the pending operation of a set, which can be a backup, a move, a catalog publication, or a storage-class change:

```sh
glesha retry docs
```

If you moved a finished archive before the upload completed, give its new path:

```sh
glesha retry docs --archive /new/location/backup.tar.xz.gpg
```

To discard the pending operation, run `glesha catalog abandon docs`.

### Change the storage class of a snapshot

This guide applies only to AWS.

```sh
glesha storage-class docs --snapshot SNAPSHOT_ID --class DEEP_ARCHIVE
```

Without `--snapshot`, the command changes the latest snapshot. glesha copies the object within AWS. A source object in cold storage can require a retrieval first.

To set the class of future snapshots, use `glesha configure docs --storage-class CLASS` or `--incremental-storage-class CLASS`.

### Move a set to another remote

`move` copies the complete history of a set to a new remote, verifies the copies, and makes the new remote the destination. The old copies are kept.

1. Move the set. List every source and catalog remote in `--from`:

	```sh
	glesha move docs --from aws --to b2
	```

	If old archives are in cold storage, add `--request-retrieval`.

2. Synchronize the catalog:

	```sh
	glesha catalog sync docs
	```

3. Confirm the change:

	```sh
	glesha status docs
	```

	The `Destination` line shows the new remote.

glesha does not delete the old copies. Delete them in the AWS or Backblaze console when you no longer need them.

### Free local disk space

`cleanup` removes local archive copies in the state directory that also exist on a remote. It does not remove remote backups.

1. Preview the removal:

	```sh
	glesha cleanup docs --dry-run
	```

2. Remove the files:

	```sh
	glesha cleanup docs
	```

	glesha asks for confirmation. To process every set, use `--all`.

`cleanup` reads the catalog passphrase from `GLESHA_CATALOG_PASSPHRASE` or a prompt.

### Resolve a catalog conflict

A conflict occurs when two computers publish different changes to one set. `catalog sync` then exits with status 4.

1. Review the differences:

	```sh
	glesha catalog resolve docs
	```

2. Keep one side:

	```sh
	glesha catalog resolve docs --keep remote
	```

	`--keep local` keeps the changes from this computer. glesha asks for confirmation.

Both snapshot histories are kept. If the conflict concerns the catalog location, select the correct remote with `--authority REMOTE`.

To prevent conflicts, write to each set from one computer at a time.

### Move state from an older version

Run this procedure when a command instructs you to run `glesha state migrate`.

1. Stop all other glesha processes.
2. Run the migration:

	```sh
	glesha state migrate
	```

The old and new locations must be on the same file system. Copies of the original databases are kept in `.state-migration-backup` in the new state directory. If the migration stops, run the command again. To migrate into another directory, pass a new path with `--state-dir`.

### Test glesha with separate state

Point glesha at an empty state directory:

```sh
glesha catalog sync --state-dir /tmp/glesha-test --from b2
glesha list --state-dir /tmp/glesha-test
```

The test state has its own registry and catalogs. The buckets are real, so `run`, `catalog sync SET`, and other write commands change remote data.

## Work with archives

### Encrypt a directory into one file

`archive` creates one encrypted file without a backup set or a remote. Use it to send files to someone or to store them offline.

```sh
glesha archive ~/Projects/report --output report.tar.xz.gpg
```

glesha asks for a passphrase twice. Without `--output`, the file is `glesha-TIMESTAMP.tar.xz.gpg` in the current directory.

The recipient extracts it with glesha:

```sh
glesha extract report.tar.xz.gpg --into ~/report
```

The recipient can also use GnuPG and tar. See [Open an archive without glesha](#open-an-archive-without-glesha). Send the passphrase through a different channel than the file.

To store the file in a bucket and retrieve it later:

```sh
glesha upload report.tar.xz.gpg --to b2 --key archives/report.tar.xz.gpg
glesha download archives/report.tar.xz.gpg --from b2 --output report.tar.xz.gpg
```

### Open an archive without glesha

A single-file archive is an OpenPGP message that contains a compressed tar stream. This method works for standalone archives and for full snapshots.

```sh
gpg --decrypt report.tar.xz.gpg | tar -xJf -
```

For gzip, use `tar -xzf -`. For bzip2, use `tar -xjf -`.

The archive has one top-level directory. The `.glesha-manifest-v1.json` file in that directory describes the archive and can be deleted after extraction.

Incremental snapshots contain only changed files, and chunked snapshots are split into chunks. Restore both with glesha.

### Import an existing archive

`import` adds an existing full archive to a set as a snapshot. It accepts archives from `glesha archive` and older `encb` archives. glesha does not modify or upload the archive.

Import from a bucket:

```sh
glesha import old-docs --from b2 --key backups/old-docs.tar.gz.gpg --date 2024-01-02T03:04:05Z
```

glesha downloads the archive once to build the catalog and publishes the catalog to the same bucket. `--date` records the original backup date.

For a local file, upload it with `glesha upload` first, then import it from the bucket. A set imported with only `--file` stays local.

To continue backups of the imported set, bind its roots. `glesha browse old-docs` lists the root names:

```sh
glesha configure old-docs --root Documents=~/Documents
glesha run old-docs --incremental
```
