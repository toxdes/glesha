# Configuration

Command-line options override the configuration file. The configuration file overrides built-in defaults. Credentials come only from [environment variables](#credentials).

Options passed to `create` or `configure`, such as `--compression` or `--to`, are saved with the set and apply to later runs.

## Configuration file

The configuration file uses TOML. Default location:

| Platform | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/glesha/config.toml`, or `~/.config/glesha/config.toml` if `XDG_CONFIG_HOME` is not set |
| macOS | `~/Library/Application Support/glesha/config.toml` |
| Windows | `%AppData%\glesha\config.toml` |

`--config FILE` selects another file. A missing default file is not an error. A missing `--config` file is an error. glesha ignores keys that it does not recognize.

### Minimal configuration

glesha runs without a configuration file. The bucket settings can come from [environment variables](#environment-variables). To keep them in the file instead, set one remote.

For Backblaze B2:

```toml
[b2]
bucket = "my-backups"
endpoint = "https://s3.us-west-004.backblazeb2.com"
```

For Amazon S3:

```toml
[aws]
bucket = "my-backups"
region = "eu-west-1"
```

Every other key is optional.

### Sections

| Section | Controls | Read |
| --- | --- | --- |
| [`[aws]`](#aws) | The `aws` remote. | By every command that accesses the remote. |
| [`[b2]`](#b2) | The `b2` remote. | By every command that accesses the remote. |
| [`[remotes.NAME]`](#remotesname) | Additional remotes. | By every command that accesses the remote. |
| [`[backup]`](#backup) | Storage classes of new sets. | By `create`. |
| [`[archive]`](#archive) | Archive name prefix, compression, and build mode. | `prefix`, `compression`, and `compression_level` by `create` and `archive`. `mode` by `run` and `archive`. |
| [`[catalog]`](#catalog) | Catalog encryption, catalog keys, and the state directory. | By every command. |
| [`[cold]`](#cold) | Retrievals from AWS cold storage. | By `restore`, `import`, and `move`. |
| [`[memory]`](#memory) | Memory budget. | By every command. |
| [`[workers]`](#workers) | Parallel hashing and transfers. | By every command. |

[Complete file](#complete-file) lists every key with its default.

## Remotes

### [aws]

The `aws` remote, for Amazon S3 or another S3-compatible service.

```toml
[aws]
bucket = "my-backups"
region = "us-east-1"
storage_class = "STANDARD"
```

| Key | Type | Default | Environment variable | Description |
| --- | --- | --- | --- | --- |
| `bucket` | string | none | `AWS_BUCKET_NAME` | Bucket name. |
| `region` | string | `"us-east-1"` | `AWS_REGION` | Bucket region. |
| `storage_class` | string | `"STANDARD"` | `AWS_STORAGE_CLASS` | Storage class for uploads. See [Storage classes](#storage-classes). |
| `endpoint` | string | none | none | Custom S3 endpoint URL. An endpoint also turns on path-style requests. |

An environment variable applies only when the key is not set in the file. `kind` is ignored in this section.

### [b2]

The `b2` remote, for Backblaze B2 through its S3-compatible API.

```toml
[b2]
bucket = "my-backups"
endpoint = "https://s3.us-west-004.backblazeb2.com"
```

| Key | Type | Default | Environment variable | Description |
| --- | --- | --- | --- | --- |
| `bucket` | string | none | `B2_BUCKET_NAME` | Bucket name. |
| `endpoint` | string | none | `B2_BUCKET_ENDPOINT` | Required. S3-compatible endpoint of the bucket. If the value has no scheme, glesha adds `https://`. |
| `region` | string | Taken from the endpoint | none | Bucket region. For the endpoint `s3.us-west-004.backblazeb2.com`, the region is `us-west-004`. |

An environment variable applies only when the key is not set in the file. `kind` and `storage_class` are ignored in this section. B2 has no storage classes.

### [remotes.NAME]

A remote named `NAME`. Use it for a second bucket of the same kind.

```toml
[remotes.offsite]
kind = "b2"
bucket = "offsite-backups"
endpoint = "https://s3.eu-central-003.backblazeb2.com"
region = "eu-central-003"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `kind` | string | none | Required. `aws` or `b2`. |
| `bucket` | string | none | Required. Bucket name. |
| `region` | string | none | Bucket region. |
| `endpoint` | string | none | Endpoint URL. Required for `b2`. |
| `storage_class` | string | none | AWS storage class for uploads. |

Environment variables do not set these keys. glesha does not take the region from the endpoint or add `https://` to it, so set both for a `b2` remote. All remotes of one kind use the same credentials. `[remotes.aws]` and `[remotes.b2]` replace the `[aws]` and `[b2]` sections.

## Backups

### [backup]

The storage classes that `create` saves with a new set, and how long `delete` keeps a set in the trash. Changing the storage classes does not change existing sets. To change a set, use `glesha configure`.

```toml
[backup]
full_storage_class = "STANDARD_IA"
incremental_storage_class = "STANDARD_IA"
trash_period = "24h"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `full_storage_class` | string | none | AWS storage class for full snapshots. |
| `incremental_storage_class` | string | none | AWS storage class for incremental snapshots. |
| `trash_period` | duration | `"24h"` | How long a deleted set stays in the trash, for example `"30m"` or `"72h"`. `"0"` makes `delete` permanent at once, like `delete --now`. Other values must be at least `"1s"`. |

`create --storage-class` and `create --incremental-storage-class` override these keys. Without a class, glesha uses the [order in Storage classes](#storage-classes).

### [archive]

The format of new archives. `create` saves `prefix`, `compression`, and `compression_level` with a new set, so changing them does not change existing sets. `glesha archive` reads all four keys. `run` reads `mode`.

```toml
[archive]
prefix = "glesha"
compression = "xz"
compression_level = 6
mode = "auto"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `prefix` | string | `"glesha"` | Prefix for archive names and remote archive keys. Allowed values: `glesha`, `encb`. |
| `compression` | string | `"xz"` | Allowed values: `xz`, `gzip`, `bzip2`. |
| `compression_level` | integer | `6` | From `1` to `9`. Higher levels compress more and use more memory. |
| `mode` | string | `"auto"` | Where glesha holds the encrypted archive while it builds it. See [Archive modes](#archive-modes). |

`--prefix`, `--compression`, `--compression-level`, and `--archive-mode` override these keys.

For xz, the dictionary size is 64 KiB at level 1 and doubles with each level, to 16 MiB at level 9. These levels differ from the presets of the `xz` tool. High levels can require a larger `[memory] max`.

#### Archive modes

| Value | Behavior |
| --- | --- |
| `auto` | Keep the archive in memory while it fits in the memory budget, then move it to a temporary file next to the output. |
| `memory` | Keep the archive in memory. The command fails if the archive does not fit in the memory budget. |
| `stream` | Write the archive to a temporary file from the start. |

### [catalog]

Catalog encryption, the location of catalog objects in each bucket, and the local state directory.

```toml
[catalog]
encrypted = true
prefix = "glesha/v2"
directory = "/path/to/state"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `encrypted` | boolean | `true` | Encrypt remote catalogs with the catalog passphrase. With `false`, glesha asks for no catalog passphrase. |
| `prefix` | string | `"glesha/v2"` | Key prefix for catalog objects in each bucket. |
| `directory` | string | Platform state directory | Local state directory. See [Local state](#local-state). |

`--state-dir` overrides `directory`.

### [cold]

Retrievals from AWS cold storage.

```toml
[cold]
days = 3
tier = "Bulk"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `days` | integer | `3` | Number of days that AWS keeps the temporary readable copy. Must be 1 or more. |
| `tier` | string | `"Bulk"` | Retrieval tier: `Bulk`, `Standard`, or `Expedited`. The value is not case-sensitive. |

`--days` and `--retrieval` override these keys. A pending retrieval keeps its original duration.

## Performance

### [memory]

The memory budget for one command.

```toml
[memory]
max = "512MiB"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `max` | string | Half of the available memory on Linux. 128 MiB on other systems. | Memory budget. The minimum is 48 MiB. |

`--memory-max` overrides `max`.

A size is a whole number with an optional unit: `KiB`, `MiB`, or `GiB` for binary units, or `KB`, `MB`, or `GB` for decimal units. A number without a unit is bytes. A small budget reduces the number of parallel transfers below `[workers] transfer`.

### [workers]

The number of parallel workers.

```toml
[workers]
hash = 4
transfer = 4
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `hash` | integer | `4` | Maximum number of files that glesha hashes at the same time. |
| `transfer` | integer | `4` | Maximum number of parallel transfers, such as parts of a multipart upload. |

`--hash-workers` overrides `hash`. `-j`, `--jobs`, and `--transfer-workers` override `transfer`.

Both values are upper limits and must be 1 or more. Archiving always uses one worker. Downloads transfer one object at a time.

## Complete file

Every key with its default. Lines that start with `#` show keys without a default.

```toml
[aws]
# bucket = "my-backups"
region = "us-east-1"
storage_class = "STANDARD"
# endpoint = "https://s3.example.com"

[b2]
# bucket = "my-backups"
# endpoint = "https://s3.us-west-004.backblazeb2.com"
# region = "us-west-004"

# [remotes.offsite]
# kind = "b2"
# bucket = "offsite-backups"
# endpoint = "https://s3.eu-central-003.backblazeb2.com"
# region = "eu-central-003"
# storage_class = "STANDARD"

[backup]
# full_storage_class = "STANDARD_IA"
# incremental_storage_class = "STANDARD_IA"

[archive]
prefix = "glesha"
compression = "xz"
compression_level = 6
mode = "auto"

[catalog]
encrypted = true
prefix = "glesha/v2"
# directory = "/path/to/state"

[cold]
days = 3
tier = "Bulk"

[memory]
# max = "512MiB"

[workers]
hash = 4
transfer = 4
```

## Storage classes

glesha accepts these AWS storage classes:

- `STANDARD`
- `STANDARD_IA`
- `ONEZONE_IA`
- `INTELLIGENT_TIERING`
- `GLACIER_IR`
- `GLACIER`
- `DEEP_ARCHIVE`
- `REDUCED_REDUNDANCY`

Objects in `GLACIER`, `DEEP_ARCHIVE`, and the archive tiers of `INTELLIGENT_TIERING` require a retrieval before glesha can read them. `DEEP_ARCHIVE` does not support the `Expedited` tier.

A class other than `STANDARD` requires at least one AWS destination. B2 remotes ignore the class.

For a new snapshot, glesha uses the first class that is set:

1. `run --storage-class`.
2. The set's saved class: `--incremental-storage-class` for incremental snapshots, then `--storage-class`. These come from `create`, `configure`, or the `[backup]` section.
3. The `storage_class` of the first AWS remote in the destination list.
4. `STANDARD`.

## Credentials

glesha reads credentials from environment variables. `--env FILE` loads variables from a file.

### Environment variables

| Variable | Used for |
| --- | --- |
| `AWS_ACCESS_KEY_ID` | AWS access key. `AWS_ACCESS_KEY` is accepted as another name. |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key. `AWS_SECRET_KEY` is accepted as another name. |
| `AWS_SESSION_TOKEN` | Session token for temporary AWS credentials. |
| `AWS_BUCKET_NAME` | Default for `[aws] bucket`. |
| `AWS_REGION` | Default for `[aws] region`. |
| `AWS_STORAGE_CLASS` | Default for `[aws] storage_class`. |
| `B2_KEY_ID` | B2 application key ID. |
| `B2_APPLICATION_KEY` | B2 application key. |
| `B2_BUCKET_NAME` | Default for `[b2] bucket`. |
| `B2_BUCKET_ENDPOINT` | Default for `[b2] endpoint`. |
| `GLESHA_CATALOG_PASSPHRASE` | Catalog passphrase. See [Passphrases](#passphrases). |

If credentials are missing, the error message names the missing variables.

### Credentials file

A credentials file has one `NAME=VALUE` assignment on each line:

```dotenv
# Backblaze B2
B2_KEY_ID=your-key-id
B2_APPLICATION_KEY=your-application-key
B2_BUCKET_NAME=my-backups
B2_BUCKET_ENDPOINT=https://s3.us-west-004.backblazeb2.com

# AWS
export AWS_ACCESS_KEY_ID=your-access-key
export AWS_SECRET_ACCESS_KEY=your-secret-key
AWS_REGION=eu-west-1
```

Parsing rules:

- Blank lines and lines that start with `#` are ignored.
- A leading `export ` is ignored.
- Single or double quotes around a value are removed.
- A variable that is already set in the environment keeps its value.

Restrict access to the file, for example with `chmod 600`.

## Passphrases

The archive passphrase comes from one of these sources:

- `--passphrase-file FILE`. Use `-` to read it from standard input.
- A hidden prompt, if standard input is a terminal.

The catalog passphrase comes from the first of these sources that is set:

1. `--catalog-passphrase-file FILE`. Use `-` to read it from standard input.
2. The `GLESHA_CATALOG_PASSPHRASE` environment variable.
3. A hidden prompt, if standard input is a terminal.

A passphrase file holds only the passphrase. One trailing newline is removed. The passphrase cannot be empty, and it must be smaller than 1 MiB.

Without a terminal, file, or variable, the command fails instead of waiting for input.

If snapshots of a set use different archive passphrases, pass `--archive-passphrase-file SNAPSHOT_ID=FILE` to `restore` for each snapshot that differs.

glesha does not store passphrases.

## Local state

The state directory holds the registry of sets, the catalog databases, unfinished operations, and some archive copies. Default location:

| Platform | Path |
| --- | --- |
| Linux | `$XDG_DATA_HOME/glesha/state`, or `~/.local/share/glesha/state` if `XDG_DATA_HOME` is not set |
| macOS | `~/Library/Application Support/glesha/state` |
| Windows | `%LOCALAPPDATA%\glesha\state` |

`[catalog] directory` and `--state-dir DIR` select another directory. `--config` does not change the state directory. To test with separate state, see [Test glesha with separate state](guides.md#test-glesha-with-separate-state).

### Older state locations

Versions before 0.5.2 stored state in `glesha/state-v2` in the configuration directory. If that directory has state and `[catalog] directory` is not set, commands stop with an instruction to run `glesha state migrate`. See [Move state from an older version](guides.md#move-state-from-an-older-version).

## Output settings

These global options control output.

| Option | Values | Default | Description |
| --- | --- | --- | --- |
| `--format` | `plain`, `json`. `status` also accepts `table` and `csv`. | `plain` | Result format on standard output. See [Structured output](cli-reference.md#structured-output). |
| `--color` | `auto`, `yes`, `no` | `auto` | Color in human output. `always` and `never` are accepted as other names for `yes` and `no`. |
| `--progress` | `auto`, `plain`, `off` | `auto` | Progress display on standard error. |
| `-L`, `--log-level` | `debug`, `info`, `warn`, `error`, `silent` | `info` | Amount of diagnostic output. `warn` and higher also hide progress. |

