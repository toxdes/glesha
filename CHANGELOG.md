# Changelog

## Unreleased

### Added

- JSON results for every command, with structured progress and errors on stderr.
- `catalog sync` and conflict resolution, plus confirmed cleanup of eligible local archives.
- Status summaries with storage costs and cached cold-retrieval readiness.
- Explicit migration of local state out of the configuration directory.

### Changed

- Imports publish their catalog automatically when a remote destination is configured.
- New cold-storage retrievals keep the temporary readable copy for three days instead of seven.
- Human-readable timestamps use the local timezone.

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
