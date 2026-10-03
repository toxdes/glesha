package storage_class_cmd

import (
	"context"

	"glesha/cmd/app_cmd"
)

func Execute(ctx context.Context, args []string, version, sha string) error {
	return app_cmd.Execute(ctx, append([]string{"storage-class"}, args...), version, sha)
}
