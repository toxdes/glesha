package cmd

import (
	"context"

	"glesha/cmd/app_cmd"
	"glesha/cmd/archive_cmd"
	"glesha/cmd/browse_cmd"
	"glesha/cmd/catalog_cmd"
	"glesha/cmd/check_cmd"
	"glesha/cmd/configure_cmd"
	"glesha/cmd/create_cmd"
	"glesha/cmd/decrypt_cmd"
	"glesha/cmd/download_cmd"
	"glesha/cmd/extract_cmd"
	"glesha/cmd/help_cmd"
	"glesha/cmd/history_cmd"
	"glesha/cmd/import_cmd"
	"glesha/cmd/list_cmd"
	"glesha/cmd/move_cmd"
	"glesha/cmd/restore_cmd"
	"glesha/cmd/retry_cmd"
	"glesha/cmd/run_cmd"
	"glesha/cmd/status_cmd"
	"glesha/cmd/storage_class_cmd"
	"glesha/cmd/upload_cmd"
	"glesha/cmd/version_cmd"
)

func Execute(ctx context.Context, args []string, version, sha string) error {
	name, rest, err := app_cmd.ResolveCommand(args)
	if err != nil {
		return err
	}
	switch name {
	case "version":
		return version_cmd.Execute(ctx, rest, version, sha)
	case "help":
		return help_cmd.Execute(ctx, rest, version, sha)
	case "create":
		return create_cmd.Execute(ctx, rest, version, sha)
	case "configure":
		return configure_cmd.Execute(ctx, rest, version, sha)
	case "run":
		return run_cmd.Execute(ctx, rest, version, sha)
	case "retry":
		return retry_cmd.Execute(ctx, rest, version, sha)
	case "list":
		return list_cmd.Execute(ctx, rest, version, sha)
	case "status":
		return status_cmd.Execute(ctx, rest, version, sha)
	case "history":
		return history_cmd.Execute(ctx, rest, version, sha)
	case "browse":
		return browse_cmd.Execute(ctx, rest, version, sha)
	case "restore":
		return restore_cmd.Execute(ctx, rest, version, sha)
	case "import":
		return import_cmd.Execute(ctx, rest, version, sha)
	case "move":
		return move_cmd.Execute(ctx, rest, version, sha)
	case "archive":
		return archive_cmd.Execute(ctx, rest, version, sha)
	case "decrypt":
		return decrypt_cmd.Execute(ctx, rest, version, sha)
	case "extract":
		return extract_cmd.Execute(ctx, rest, version, sha)
	case "upload":
		return upload_cmd.Execute(ctx, rest, version, sha)
	case "download":
		return download_cmd.Execute(ctx, rest, version, sha)
	case "catalog":
		return catalog_cmd.Execute(ctx, rest, version, sha)
	case "storage-class":
		return storage_class_cmd.Execute(ctx, rest, version, sha)
	case "check":
		return check_cmd.Execute(ctx, rest, version, sha)
	}
	return app_cmd.Execute(ctx, args, version, sha)
}
func ExitCode(err error) int { return app_cmd.ExitCode(err) }
