package version_cmd

import (
	"context"
	L "glesha/logger"
)

// NOTE: populated at build time with -ldflags (-X)
var version string

// NOTE: populated at build time with -ldflags (-X)
var commitHash string

func Execute(ctx context.Context, args []string) error {
	name := ctx.Value("values").(map[string]string)["binary_name"]
	L.Printf("%s version v%s, build %s\n", name, version, commitHash)
	return nil
}
