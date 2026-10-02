package status_cmd

import (
	"context"

	"glesha/cmd/app_cmd"
)

func Execute(ctx context.Context, args []string, version, sha string) error {
	return app_cmd.Execute(ctx, append([]string{"status"}, args...), version, sha)
}
