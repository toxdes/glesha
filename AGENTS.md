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
- Repositories initialized with `NewXxxRepository(db)`
- `Configurator` interface in `config/interface.go` for test mocking of package-level functions

## Code Style
- Always use `logger` package with `L` import alias (`L "glesha/logger"`)
- Follow existing patterns from `add_cmd/add.go` and `run_cmd/run.go`
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
- Use `*CmdEnv` naming for command environment structs, stored as package-level singleton (`var addCmdEnv *AddCmdEnv`)
- Interface names: `*er` / `*or` suffix (e.g., `Archiver`, `Configurator`, `StorageFactory`)
- Enum constants: use custom types with `String()` / `Parse()` methods, not raw strings or ints
- Status constants prefix: `TASK_STATUS_*`, `UPLOAD_STATUS_*`, `STATUS_*`, `AF_*`, `PROVIDER_*`
- No builder pattern or functional options — use simple struct literal initialization

## Error Handling
- Return descriptive errors
- Use `fmt.Errorf` with context
- Validate inputs before processing
- Use exported `Err*` variables for package-level sentinel errors (e.g., `var ErrNoExistingTask = errors.New("no existing task")`)
- Error message format: `"package: descriptive message: %w"` (e.g., `"aws: could not create storage bucket: %w"`)

## Avoid Duplication
- Leverage existing functions:
  - `config.Get()` - get current config
  - `config.ToJson()` - marshal config
  - `config.GetDefaultConfigPath()` - get default path
  - `config.Parse()` - parse config file
  - `config.ParseProvider()` - parse provider string
  - `config.ParseArchiveFormat()` - parse archive format
  - `file_io.WriteToFile()` - write files
  - `file_io.IsReadable()` - check readability
  - `L.SetColorModeFromString()` - set color mode
  - `L.SetLevelFromString()` - set log level

## Conventions
- DB init sequence: `database.GetDBFilePath(ctx)` → `database.NewDB(dbPath)` → `db.Init(ctx)` → `defer db.Close(ctx)`
- Flag parsing: parse `--log-level`/`-L` and `--color` first in every command using `flag.NewFlagSet()`
- Expand `~` in all path flags using `os.UserHomeDir()`
- Use `database.ToTimeStr()` / `database.FromTimeStr()` for time formatting
- Concurrency: channel semaphore + `sync.WaitGroup` + `ctx.Done()` select for worker pools
- Use `sync.Map` for cross-goroutine progress tracking, `sync/atomic` for counters
- Use `sync.Mutex` for protecting shared state
- Status lifecycle: set RUNNING → do work → on error set ABORTED/FAILED → on success set COMPLETED

## Build 
use `./scripts/build.sh` to build. the binary will be created at `./build/glesha` which can be used for further testing.

## Testing
- use `./scripts/test.sh` to run tests
- Use table-driven tests with `t.Run()` subtests
- Use in-memory SQLite (`:memory:`) for database tests
- Use white-box testing (`package foo`, not `package foo_test`)
- Use `testify/mock` for interface mocking
- Use `httptest.Server` for HTTP mocking
- Extract test helpers (e.g., `setupTestDB(t *testing.T)`)

