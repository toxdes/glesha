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

`--config FILE` selects another file. A missing default file is not an error. A missing `--config` file is an error.

Example:

```toml
[b2]
bucket = "my-backups"
endpoint = "https://s3.us-west-004.backblazeb2.com"

[memory]
max = "512MiB"
```

### [archive]

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `prefix` | string | `"glesha"` | Prefix for new archive names and remote archive keys. Allowed values: `glesha`, `encb`. |
| `compression` | string | `"xz"` | Compression for new archives. Allowed values: `xz`, `gzip`, `bzip2`. |
| `compression_level` | integer | `6` | Compression level from `1` to `9`. Higher levels compress more and use more memory. |
| `mode` | string | `"auto"` | Where glesha holds the encrypted archive while it builds it. See [Archive modes](#archive-modes). |

For xz, the dictionary size is 64 KiB at level 1 and doubles with each level, to 16 MiB at level 9. These levels differ from the presets of the `xz` tool. High levels can require a larger `[memory] max`.

#### Archive modes

| Value | Behavior |
| --- | --- |
| `auto` | Keep the archive in memory while it fits in the memory budget, then move it to a temporary file next to the output. |
| `memory` | Keep the archive in memory. The command fails if the archive does not fit in the memory budget. |
| `stream` | Write the archive to a temporary file from the start. |

### [memory]

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `max` | string | Half of the available memory on Linux. 128 MiB on other systems. | Memory budget for one command. The minimum is 48 MiB. |

A size is a whole number with an optional unit: `KiB`, `MiB`, or `GiB` for binary units, or `KB`, `MB`, or `GB` for decimal units. A number without a unit is bytes.

A small budget reduces the number of parallel transfers below `[workers] transfer`.

### [workers]

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `hash` | integer | `4` | Maximum number of files that glesha hashes at the same time. |
| `transfer` | integer | `4` | Maximum number of parallel transfers, such as parts of a multipart upload. |

Both values are upper limits and must be 1 or more. Archiving always uses one worker. Downloads transfer one object at a time.

### [aws]

Settings for the `aws` remote.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `bucket` | string | `AWS_BUCKET_NAME` | Bucket name. |
| `region` | string | `AWS_REGION`, or `"us-east-1"` | Bucket region. |
| `storage_class` | string | `AWS_STORAGE_CLASS`, or `"STANDARD"` | Storage class for uploads. See [Storage classes](#storage-classes). |
| `endpoint` | string | none | Custom S3 endpoint URL. Setting an endpoint also turns on path-style requests. |

An environment variable in the Default column applies only when the key is not set in the file.

### [b2]

Settings for the `b2` remote.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `bucket` | string | `B2_BUCKET_NAME` | Bucket name. |
| `endpoint` | string | `B2_BUCKET_ENDPOINT` | S3-compatible endpoint of the bucket, for example `https://s3.us-west-004.backblazeb2.com`. Required. If the value has no scheme, glesha adds `https://`. |
| `region` | string | Taken from the endpoint | Bucket region. For the endpoint `s3.us-west-004.backblazeb2.com`, the region is `us-west-004`. |

### [remotes.NAME]

Defines a remote named `NAME`.

```toml
[remotes.offsite]
kind = "b2"
bucket = "offsite-backups"
endpoint = "https://s3.eu-central-003.backblazeb2.com"
region = "eu-central-003"
```

| Key | Type | Description |
| --- | --- | --- |
| `kind` | string | Required. `aws` or `b2`. |
| `bucket` | string | Required. Bucket name. |
| `region` | string | Bucket region. |
| `endpoint` | string | Endpoint URL. Required for `b2`. |
| `storage_class` | string | AWS storage class for uploads. |

Environment variables do not set these keys. All remotes of one kind use the same credentials. `[remotes.aws]` and `[remotes.b2]` replace the `[aws]` and `[b2]` sections.

### [backup]

Defaults for new sets. `create --storage-class` and `create --incremental-storage-class` override these keys.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `full_storage_class` | string | none | AWS storage class for full snapshots. |
| `incremental_storage_class` | string | none | AWS storage class for incremental snapshots. |

### [catalog]

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `encrypted` | boolean | `true` | Encrypt remote catalogs with the catalog passphrase. |
| `prefix` | string | `"glesha/v2"` | Key prefix for catalog objects in each bucket. |
| `directory` | string | Platform state directory | Local state directory. See [Local state](#local-state). `--state-dir` overrides it. |

### [cold]

Defaults for retrievals from AWS cold storage.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `days` | integer | `3` | Number of days that AWS keeps the temporary readable copy. Must be 1 or more. |
| `tier` | string | `"Bulk"` | Retrieval tier: `Bulk`, `Standard`, or `Expedited`. The value is not case-sensitive. |

`--days` and `--retrieval` override these keys. A pending retrieval keeps its original duration.

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

