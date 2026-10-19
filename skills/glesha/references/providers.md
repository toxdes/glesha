# Provider setup

For "set up glesha", give the validation command first, then the few manual steps
needed to prepare it. If the provider is unknown, start with `glesha help check`
and ask AWS or B2. There is no interactive `glesha setup` command.

Never collect secret values in chat. The user supplies them through process
variables or a private env file at a location they choose. Do not create or edit
that file for them. `--env FILE` accepts that path on every command;
existing process variables take precedence. Nonsecret bucket/region/endpoint
settings may instead go in the user's chosen TOML config.
Explicit TOML bucket, region and endpoint settings take priority over their
environment fallbacks.

## AWS S3

Suggested first line when AWS is known:

`glesha check --to aws`

1. Choose a private S3 bucket and its actual region.
2. Create credentials restricted to that bucket with list, object read/version
   read, upload and multipart-abort access. `check` additionally needs Object Lock
   configuration inspection and deletion of its temporary object's exact version.
3. Set the following locally, then run the command above:

| Variable | Value |
| --- | --- |
| `AWS_ACCESS_KEY_ID` | Access key ID |
| `AWS_SECRET_ACCESS_KEY` | Secret access key |
| `AWS_SESSION_TOKEN` | Only for temporary credentials |
| `AWS_BUCKET_NAME` | Bucket name |
| `AWS_REGION` | Region such as `ap-southeast-1` |

Explain `check` and `--to aws`; if an env file path is known, append and explain
`--env FILE`. Do not suggest `aws configure` or IAM role/profile authentication
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

Suggested first line when B2 is known:

`glesha check --to b2`

1. Create/select a private B2 bucket and copy its S3 endpoint.
2. Create a bucket-restricted application key with `listBuckets`, `listFiles`,
   `readFiles`, `writeFiles` and `deleteFiles`; the S3-compatible API needs an
   application key, not the master application key. Retention inspection can also
   require `readBucketRetentions`.
3. Set these locally, then run the command above:

| Variable | Value |
| --- | --- |
| `B2_KEY_ID` | Application key ID |
| `B2_APPLICATION_KEY` | Application key |
| `B2_BUCKET_NAME` | Bucket name |
| `B2_BUCKET_ENDPOINT` | S3 endpoint, e.g. `https://s3.us-west-004.backblazeb2.com` |

Explain `check` and `--to b2`; append `--env FILE` only for a supplied file path.
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
