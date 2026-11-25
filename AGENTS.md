# Coding Standards
## Comments
- Very short and concise
- Only for implicit assumptions or non-self-explanatory code
- Follow existing comment style (e.g., `// NOTE:` for important notes)
- Inline comments should be lowercase (e.g., `// parse cli args`), not sentence case

## Package Structure
- `main.go` stays at the repository root; release builds run `go build .` with `-X main.VERSION`.
- All other packages live under `internal/`; none is a public Go API.
- `internal/cli`: the command line. A command is one entry in `commands` (`commands.go`): handler, help, actions and accepted options. A flag is one entry in `options` (`options.go`): spellings, help placeholder, group, description and binding. Per-command help wording goes in the command's `overrides`.
- `internal/cloud`: storage providers. A provider is one entry in the table in `provider.go` with its `Capabilities`; behaviour that differs by provider reads a capability, never a provider name.
- `internal/cloud/s3store`: the `cloud.Store` for S3-compatible providers and the only package that imports the AWS SDK. It reports missing objects and lost conditional writes as `cloud.ErrNotFound` and `cloud.ErrConflict`; code outside it never inspects S3 error codes.
- `internal/backup`: set operations (run, restore, import, move) and catalog publication.
- `internal/database/model` (data structs + CREATE TABLE consts), `internal/database/repository` (interface + impl + `New*`).
- Repository triad: exported interface → unexported struct → exported `New*` constructor that returns the interface.
- Catalog repositories are opened with `repository.NewCatalogRepository(ctx, path)`.

## Code Style
- Always prefer idiomatic Go. Do not alias imports unless names collide.
- Use `internal/fsutil` for file operations; extend it when a new one is needed.
- Use `internal/logger` for logs and progress.
- Package-level reusable functions can go in `utils.go`, only if they have >=2 callers.
- Expand `~` in paths with `fsutil.Expand()`.
- Validate file readability with `fsutil.IsReadable()`.
- Import ordering: stdlib → external → internal groups, with blank lines separating each group.
- If a function might involve async work, accept `ctx context.Context` as its first parameter.
- Use `defer` for cleanup (closing db, files, etc.).
- Parsed flags live in `AppCmdEnv` (`internal/cli/app.go`), one field per flag, named after the flag; read presence with `given`, never `present` directly.
- Interfaces: name single-method interfaces with an `-er` suffix; name larger ones after what they are.
- Enum constants: use custom types with `String()` / `Parse()` methods, not raw strings or ints.
- Snapshot and transfer status constants use the `STATUS_*` prefix.
- No builder pattern or functional options — use simple struct literal initialization.
- Keep functions short enough to read in one screen; split long operations into named steps.

## Error Handling
- Return descriptive errors
- Use `fmt.Errorf` with context
- Validate inputs before processing
- Use exported `Err*` variables for package-level sentinel errors (e.g., `var ErrNoExistingTask = errors.New("no existing task")`)
- Error message format: `"package: descriptive message: %w"` (e.g., `"cloud: could not create storage bucket: %w"`). Errors about a catalog database use `catalog:`, the term users see, rather than `repository:`.

## Avoid Duplication
- Leverage existing functions:
  - `config.Defaults()` - get built-in defaults
  - `config.DefaultPath()` - get the default TOML path
  - `config.Load()` - load and validate TOML
  - `config.Env()` - load credentials without overwriting process variables
  - `fsutil.Expand()` - expand home-directory paths
  - `fsutil.WriteToFile()` - write files
  - `fsutil.IsReadable()` - check readability
  - `logger.SetColorModeFromString()` - set color mode
  - `logger.SetLevelFromString()` - set log level

## Conventions
- Catalog init sequence: `repository.NewCatalogRepository(ctx, path)` → `defer catalog.Close()`
- `Execute` applies `--log-level` and `--color` once, before any command runs.
- Expand `~` in all path flags; `AppCmdEnv.expandPaths` does this for the shared ones.
- Use UTC `time.Time` values for catalog timestamps.
- Concurrency: channel semaphore + `sync.WaitGroup` + `ctx.Done()` select for worker pools
- Use `sync.Map` for cross-goroutine progress tracking, `sync/atomic` for counters
- Use `sync.Mutex` for protecting shared state
- Status lifecycle: set RUNNING → do work → on error set ABORTED/FAILED → on success set COMPLETED

## Build
- Use `CGO_ENABLED=0 go build -o build/glesha .`.
- Use Yesb only after feature completion and release readiness, never for development checks.
- Release builds use the pinned, unmodified Yesb submodule: `./yesb/build_all.py`.
- Never publish releases, commit, or push without explicit instructions.

## Testing
- Use `go test ./...` and `go test -race ./...`.
- Use table-driven tests with `t.Run()` subtests.
- Use in-memory SQLite (`:memory:`) for database tests.
- Use white-box testing (`package foo`, not `package foo_test`).
- Use `testify/mock` for interface mocking and `httptest.Server` for HTTP mocks.
- External GPG/tar tools are allowed only in interoperability tests.
- The production executable must work with an empty executable search path.

## Compatibility
- Follow `compatibility.md` when changing archive formats, schemas, models or CLI behavior.
- Preserve supported historical fixtures and serialized field names.
- Storage changes require explicit versioning and a tested migration/conversion path.
- Unknown versions must fail without modifying user data.
- Keep CLI aliases and versioned JSON stable; human formatting may evolve independently.

## Documentation
- `docs/` is the only copy of the user documentation. The website renders it unchanged.
- Update `docs/cli-reference.md` in the same change as any command, flag, default, or output change.
- Update examples in the other `docs/` pages when the behavior they show changes.
- Link between pages with relative `.md` links.

## Changelog
- Record user-visible changes under `Unreleased` in `CHANGELOG.md`.
- For a release, move those notes under `## VERSION - YYYY-MM-DD` and keep an empty `Unreleased` section. The website renders released entries from this file.
