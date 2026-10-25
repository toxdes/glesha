# Workflow details

## Import and recovery

- Use the exact legacy object key, including any existing prefix. Import preserves
  encrypted bytes and publishes metadata when a remote destination is configured.
- Cold imports/restores may wait for AWS retrieval. Keep consent prompts; only add
  `--request-retrieval` for explicit authorization. Repeat the original command to
  continue after confirmation: it can download and publish, not just inspect.
  Use `glesha status SET --refresh` for a readiness check without retrieval.
  Bulk is cheapest; faster tiers are opt-in.
- Archive/catalog passphrases are separate. Restore accepts
  `--archive-passphrase-file SNAPSHOT_ID=FILE` for mixed-password chains.
- Imported source mappings are unnecessary for browse/restore, required for runs.
  Read `configure` options before proposing mappings or a new starting snapshot.

## Maintenance

| Need | Command / constraint |
| --- | --- |
| Discover sets on another machine | Run `glesha list --remote --from PROVIDER`; confirm before `glesha catalog sync SET` |
| Sync all registered remote-backed sets | Confirm `glesha catalog sync --all`; can publish, does not discover unknown sets |
| Metadata conflict | `glesha catalog resolve SET` previews; ask before `--keep local` or `--keep remote`; no blanket force |
| Remove eligible local archives | `glesha cleanup SET` confirms; keeps pending work, explicit outputs and remote backups |
| Limited temporary disk space | `glesha run SET --chunked --spool-max 128M`; independent encrypted chunks |
| Change AWS class | `glesha storage-class SET --snapshot ID --class CLASS`; chunked snapshots cannot be retiered |

Retry reuses completed work; do not promise byte-level download or multipart resume.
Chunked snapshots currently cannot be moved between providers either.

## Tool results

Results are JSON Lines on stdout; diagnostics use stderr. Check exit codes too:
0 success, 1 failure, 2 usage, 3 pending, 4 conflict. Empty listings may emit no
records. JSON never authorizes deletions/retrieval or supplies passphrases.
