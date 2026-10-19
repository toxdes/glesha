# Intent to command

These examples assume a POSIX shell. The command is always the first response
line. Replace example values only with information the user supplied or confirmed.
Omit `--env` when the user uses process variables; repeat it for each command that
needs settings or credentials from a supplied file. Hidden passphrase prompts are the default.

## Missing destination

User: "Back up ~/Documents using glesha."

`glesha help create`

- `help create`: shows how to register a backup set.

Which provider and set name should I use? For example, B2 and `docs`.

Do not turn an unspecified cloud backup into a local `archive` operation.

## New cloud backup

User: "Back up ~/Documents to B2 as docs. My env file is /secure/cloud.env."

`glesha create docs ~/Documents --to b2 --env /secure/cloud.env`

`glesha run docs --env /secure/cloud.env`

- `create docs ~/Documents`: binds `docs` to that source directory locally.
- `--to b2`: saves B2 as the destination.
- `run docs`: creates and uploads the first full snapshot.
- `--env /secure/cloud.env`: loads provider settings and credentials; process variables win.

`create` makes no cloud requests but needs the provider's bucket/endpoint settings
to bind its destination. A custom `--config FILE` belongs on both commands so they
share the same settings and state.

## Provider check

User: "Is glesha set up OK for AWS and B2? Use /secure/cloud.env."

`glesha check --to aws,b2 --env /secure/cloud.env`

- `check`: tests provider access using a temporary write, metadata read, download and deletion.
- `--to aws,b2`: checks both configured providers.
- `--env /secure/cloud.env`: uses that credential file.

This proposes a check; it does not mean either provider passed.

## Incremental

User: "Back up only the changes in my existing docs set."

`glesha run docs --incremental`

- `docs`: the existing set and saved destination.
- `--incremental`: includes changes since the latest committed snapshot.

Keep deletion confirmations enabled. If the user's new directory differs from
the set's bound source, explain the mismatch and propose a new set instead.

## Import an existing encb archive

User: "Import encb-music-20240101.tar.gz.gpg from my configured AWS bucket as old-music."

`glesha import old-music --from aws --key encb-music-20240101.tar.gz.gpg`

- `old-music`: the set receiving the imported snapshot.
- `--from aws`: uses the configured AWS bucket.
- `--key ...`: the exact existing object key; import preserves its encrypted bytes.

Do not invent a bucket flag or prepend `glesha/archives/` to a legacy root key.
If "same bucket" has no established provider/key context, ask for those details.
If it is in Deep Archive, keep the retrieval consent prompt; only add
`--request-retrieval` when explicitly authorized. Metadata publication follows
successful import; a failed publication can be retried.

## Restore

User: "Restore the latest docs snapshot into ~/Recovered/docs."

`glesha restore docs --into ~/Recovered/docs`

- `docs`: the existing set; latest is the default snapshot.
- `--into ...`: a new output directory, protecting existing files.

Use `--snapshot ID` only for a requested historical snapshot. Do not insert an
overwrite flag or suggest deleting an existing destination to make the command fit.

## Inspect and maintain

| Intent | Command | Important distinction |
| --- | --- | --- |
| See backup state | `glesha status docs` | Cached local data. |
| Check cloud classes/readiness | `glesha status docs --refresh` | Metadata requests, not retrieval. |
| Browse filenames | `glesha browse docs --recursive` | Local metadata, not archive downloads. |
| Discover remote sets | `glesha list --remote --from aws` | Queries remote metadata. |
| Synchronize metadata | `glesha catalog sync docs` | Can publish; not a payload download. |
| Sync known remote-backed sets | `glesha catalog sync --all` | Includes locally registered sets only. |
| Preview a metadata conflict | `glesha catalog resolve docs` | No blanket `--force`. |
| Preview local archive cleanup | `glesha cleanup docs --dry-run` | Never deletes remote backups. |
| Resume a managed backup | `glesha retry docs` | Preserves recorded successful uploads. |
| Use bounded temporary storage | `glesha run docs --chunked --spool-max 128M` | Independent chunks, not one complete local archive. |
| Change an AWS archive's class | `glesha storage-class docs --snapshot ID --class DEEP_ARCHIVE` | Changes the selected snapshot; metadata remains Standard. |

For other commands and all options, use [the CLI reference](cli_reference.md).
Explain the positional arguments and selected flags in the actual response;
the intent table is routing guidance, not a replacement for those explanations.

## Agent execution

For a user-requested agent execution, propose the machine-readable form first:

`glesha status docs --format=json --progress=off`

- `docs`: the saved set.
- `--format=json`: versioned JSON Lines results on stdout; diagnostics on stderr.
- `--progress=off`: suppresses progress updates.

Ask for confirmation and wait before running this exact command. Read the exit
code as well as the JSON records; empty listings can produce no result records.
JSON does not approve deletions, retrieval charges or secret prompts.
