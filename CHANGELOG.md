# Changelog

## Unreleased

## 0.5.3 - 2026-11-27

- `feat(backup):` `delete` moves backup sets to the trash for 24 hours by default. `undelete` recovers them before the trash period expires. `delete --now` purges all stored versions after confirmation.
- `feat(status):` `status SET` names the other sets that use the same archives.
- `fix(import):` Reject a remote object already used by another set on the same computer.
- `fix(restore):` Keep staged files so an interrupted restore can resume.
- `fix(backup):` Name the remote in transfer errors and report every failed destination in a backup run. `upload` reports an unknown storage class before checking whether a destination supports it.
- `ui(cli):` Rewrite command help and manual pages in Unix style and make plain-text output consistent across commands.
- `docs:` Move the user docs into `docs/`, split installation by operating system, and document configuration defaults and overrides.
- `release:` Publish downloads under the shared `releases/` prefix and keep historical links working. Build APT and RPM repositories with pinned container images while signing on the host.

## 0.5.2 - 2026-10-31

### Fixed

- Include the state package in the Docker build context so release builds compile.

### Added

- JSON results for every command, with structured progress and errors on stderr.
- `catalog sync` and conflict resolution, plus confirmed cleanup of eligible local archives.
- Status summaries with storage costs and cached cold-retrieval readiness.
- Explicit migration of local state out of the configuration directory.
- Portable catalog discovery and encrypted metadata caching, with offline `catalog unlock SET`.
- `--state-dir DIR` for isolated local state and `restore SET --cancel` for unfinished local tasks.

### Changed

- Missing cloud credentials identify the required environment variables and `--env FILE`, including when a catalog's location is not configured.
- Imports publish their catalog automatically when a remote destination is configured.
- New cold-storage retrievals keep the temporary readable copy for three days instead of seven.
- Human-readable timestamps use the local timezone.
- Explicit `--from` and `--to` selections restrict all bucket access to their union.
- Catalog sync distinguishes updated, unchanged, and locked catalogs and groups shared failures.
- Catalog sync JSON uses output version 3 for `catalog_state`, `catalog_sync_summary`, and `catalog_sync_error`, replacing the earlier version 2 sync contract. Other JSON results retain version 2.
- Human output, command help, and manuals use clearer tables, semantic colors, and actionable next steps.
- Passphrase prompts support cancellation and no longer overlap progress output.

### Removed

- `--json`; use `--format=json` or `--format json`.

## 0.5.1 - 2026-10-04

### Added

- macOS Intel/Apple Silicon and Windows x64 release bundles.
- Release support for APT, RPM, AUR and Homebrew.

## 0.5.0 - 2026-10-03

### Changed

- Rewritten as a standalone Go executable for encrypted local archives and cloud backups.

### Added

- Full and incremental snapshots, metadata browsing, and AWS/B2 transfers.
- XZ, gzip and bzip2 compression, with existing encb archives remaining readable.
- Chunked backups with bounded temporary disk usage.
- AWS storage-class changes and explicit cold-storage retrieval.
