# Coding Standards
## Comments
- Very short and concise
- Only for implicit assumptions or non-self-explanatory code
- Follow existing comment style (e.g., `// NOTE:` for important notes)
- Inline comments should be lowercase (e.g., `// parse cli args`), not sentence case

## Package Structure
- CLI subcommands go in `cmd/<name>_cmd/`: a `<name>.go` with `Execute()` and a `usage.go` with `Usage()` / `PrintUsage()`
- Database layers: `database/model/` (data structs + CREATE TABLE consts), `database/repository/` (interface + impl + `New*`)
- Repository triad: exported interface → unexported struct → exported `New*` constructor that returns the interface
- Catalog repositories initialized with `repository.NewCatalogRepository(ctx, path)`
- `Configurator` interface lives in `config/interface.go`.

## Code Style
- Always use `logger` package with `L` import alias (`L "glesha/logger"`)
- Follow the context-aware flag parsing patterns in `cmd/app_cmd/`.
- Follow existing project directory structure
- Use flag parsing with `flag.NewFlagSet()`
- Use `file_io` package wherever possible, if new file io operations are needed that aren't supported by current `file_io`, extend `file_io` package to include that functionality.
- Package-level reusable functions can go in `utils.go`, only if they have >=2 callers
- Always prefer idiomatic golang.
- Expand `~` in paths using `os.UserHomeDir()`
- Validate file readability using `file_io.IsReadable()`
- Import ordering: stdlib → external → internal groups, with blank lines separating each group
- If a function might involve async work, accept `ctx context.Context` as its first parameter for future-proofing
- Use `defer` for cleanup (closing db, files, etc.)
- Command flags use `AppCmdEnv` in `cmd/app_cmd/app.go`, passed through the command runtime.
- Interface names: `*er` / `*or` suffix (e.g., `Archiver`, `Configurator`, `StorageFactory`)
- Enum constants: use custom types with `String()` / `Parse()` methods, not raw strings or ints
- Snapshot and transfer status constants use the `STATUS_*` prefix.
- No builder pattern or functional options — use simple struct literal initialization

## Error Handling
- Return descriptive errors
- Use `fmt.Errorf` with context
- Validate inputs before processing
- Use exported `Err*` variables for package-level sentinel errors (e.g., `var ErrNoExistingTask = errors.New("no existing task")`)
- Error message format: `"package: descriptive message: %w"` (e.g., `"aws: could not create storage bucket: %w"`)

## Avoid Duplication
- Leverage existing functions:
  - `config.Defaults()` - get built-in defaults
  - `config.DefaultPath()` - get the default TOML path
  - `config.Load()` - load and validate TOML
  - `config.Env()` - load credentials without overwriting process variables
  - `file_io.Expand()` - expand home-directory paths
  - `file_io.WriteToFile()` - write files
  - `file_io.IsReadable()` - check readability
  - `L.SetColorModeFromString()` - set color mode
  - `L.SetLevelFromString()` - set log level

## Conventions
- Catalog init sequence: `repository.NewCatalogRepository(ctx, path)` → `defer catalog.Close()`
- Flag parsing: parse `--log-level`/`-L` and `--color` first in every command using `flag.NewFlagSet()`
- Expand `~` in all path flags using `os.UserHomeDir()`
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
