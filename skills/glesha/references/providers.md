# Provider setup

## Discover and diagnose

For "which providers are configured?", inspect relevant local settings directly.
There is no provider-list command; `glesha list` lists backup sets, not providers.
Use an explicitly selected TOML file, or `glesha/config.toml` under the platform
user-config directory. Read only bucket/region/endpoint and named-remote settings;
do not print whole files. Check relevant process-variable presence without
printing credential values. A bucket setting identifies a configured profile;
credential presence alone does not prove the profile is usable.

Distinguish configured (profile settings exist), incomplete (missing settings or
credentials), and verified (a live check passed). Settings alone aren't verification.

For "is setup OK?", run `glesha check --to NAME` for identified profiles.
With a supplied `--env FILE`, pass it to glesha rather than displaying or sourcing
it. If profiles cannot be discovered without reading that file, check the known
AWS/B2 candidates separately; missing configuration is a diagnostic result,
not evidence that both are configured. Separate checks avoid one missing bucket
preventing the other provider's check. Don't guess custom remote names.
If nothing is discoverable, ask for a config/env path or provider choice; don't
ask permission just to run help. Report failures and missing variable names
concisely; hide credentials if an error unexpectedly contains them.

## Configure

Ask only for missing choices: provider, bucket, region/endpoint and config/env
location. There is no interactive `glesha setup`; `glesha configure SET` changes
an existing set, not credentials. Offer the smallest relevant template below.
Preserve unrelated existing settings and show a targeted diff. Confirm before
writing nonsecret TOML or a new placeholder-only env template; never overwrite
an existing credential file. Users fill secrets locally. Never request/paste
secret values in chat, put them in command arguments, or read credential files.
Create authorized env templates with owner-only permissions, outside version
control. Don't edit shell profiles or cloud-account policies.

`--env FILE` loads variables without overriding existing process values.
TOML bucket/region/endpoint settings take priority over environment fallbacks.
Use TOML for nonsecrets and env for credentials; all-env setup also works with
the variables below. Run `check` after the user fills credentials; don't create
a backup to validate setup.

## AWS S3

1. Choose a private S3 bucket and its actual region.
2. Create credentials restricted to that bucket with list, object read/version
   read, upload and multipart-abort access. `check` additionally needs Object Lock
   configuration inspection and deletion of its temporary object's exact version.
3. Supply the following locally, then run `glesha check --to aws`:

| Variable | Value |
| --- | --- |
| `AWS_ACCESS_KEY_ID` | Access key ID |
| `AWS_SECRET_ACCESS_KEY` | Secret access key |
| `AWS_SESSION_TOKEN` | Only for temporary credentials |
| `AWS_BUCKET_NAME` | Bucket name |
| `AWS_REGION` | Region such as `ap-southeast-1` |

Minimal TOML, with user-chosen values:

```toml
[aws]
bucket = "YOUR_BUCKET"
region = "YOUR_REGION"
```

Private env template; replace placeholders locally:

```dotenv
AWS_ACCESS_KEY_ID=REPLACE_LOCALLY
AWS_SECRET_ACCESS_KEY=REPLACE_LOCALLY
```

Add `AWS_SESSION_TOKEN` only for temporary credentials. Append supplied
`--config FILE`/`--env FILE` paths to the check command.
Do not suggest `aws configure` or IAM role/profile authentication
as a substitute: this glesha version reads the environment credential names above.

Permissions depend on the operation. Ordinary use includes `s3:ListBucket`,
`s3:GetObject`, `s3:GetObjectVersion`, `s3:PutObject` and
`s3:AbortMultipartUpload`. The check uses `s3:GetBucketObjectLockConfiguration`,
`s3:DeleteObject` and `s3:DeleteObjectVersion`. Paid cold retrieval additionally
needs `s3:RestoreObject`. Avoid suggesting account-wide administrator policies.
KMS-encrypted buckets may need the corresponding KMS permissions too.

Do not disable bucket retention merely to pass `check`: default retention can
prevent temporary-object cleanup. Explain a reported retention restriction and
let the user choose an appropriate bucket/policy. Keep lifecycle transitions
away from metadata; archival rules normally target `glesha/archives/` and any
separate legacy archive prefix rather than the whole bucket.

Source: [AWS S3 API permissions](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-with-s3-policy-actions.html).

## Backblaze B2

1. Create/select a private B2 bucket and copy its S3 endpoint.
2. Create a bucket-restricted application key with `listBuckets`, `listFiles`,
   `readFiles`, `writeFiles` and `deleteFiles`; the S3-compatible API needs an
   application key, not the master application key. Retention inspection can also
   require `readBucketRetentions`.
3. Supply these locally, then run `glesha check --to b2`:

| Variable | Value |
| --- | --- |
| `B2_KEY_ID` | Application key ID |
| `B2_APPLICATION_KEY` | Application key |
| `B2_BUCKET_NAME` | Bucket name |
| `B2_BUCKET_ENDPOINT` | S3 endpoint, e.g. `https://s3.us-west-004.backblazeb2.com` |

Minimal TOML, with user-chosen values:

```toml
[b2]
bucket = "YOUR_BUCKET"
endpoint = "https://s3.YOUR_REGION.backblazeb2.com"
```

Private env template; replace placeholders locally:

```dotenv
B2_KEY_ID=REPLACE_LOCALLY
B2_APPLICATION_KEY=REPLACE_LOCALLY
```

Append supplied `--config FILE`/`--env FILE` paths to the check command.
Glesha infers the B2 region from the endpoint when it is not configured in TOML.
Do not confuse B2 account IDs, native API URLs or download URLs with these values.

Deletion must remove the temporary object's version. B2 versioning itself is not
an error; a delete marker is not sufficient cleanup. A failed cleanup can leave
the reported test key/version behind. Preserve that identifier in the explanation.

Sources: [B2 S3-compatible keys](https://www.backblaze.com/docs/cloud-storage-s3-compatible-app-keys),
[B2 access-control mapping](https://www.backblaze.com/docs/cloud-storage-map-iam-policies-and-s3-acls-to-backblaze-b2-access-controls).

## Passwords and optional settings

Archive encryption and catalog encryption use separate passphrases. Hidden prompts
are the default. For unattended use, explain `--passphrase-file FILE` and
`--catalog-passphrase-file FILE` when the user asks; never put a password inline.
`GLESHA_CATALOG_PASSPHRASE` is the catalog environment variable.

`AWS_STORAGE_CLASS` or TOML can set AWS storage defaults. `--storage-class CLASS`
overrides archive creation when supported. Catalog metadata uses S3 Standard;
Intelligent-Tiering and Deep Archive are archive-policy choices, not setup requirements.

Named S3-compatible remotes use `[remotes.NAME]` in TOML with `kind = "aws"` or
`"b2"`, bucket, region and endpoint. They use the adapter's environment credentials.
Do not claim every S3-compatible service supports AWS-specific storage classes or
cold retrieval. Do not add an unsupported command-line endpoint/bucket flag.
