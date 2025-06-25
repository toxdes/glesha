package version_cmd

import (
	"context"
	L "glesha/logger"
)

// will be populated at build time through -ldflags
var version string
var commitHash string

func Execute(ctx context.Context, args []string) error {
	name := ctx.Value("values").(map[string]string)["binary_name"]
	L.Printf("%s version v%s, build %s\n", name, version, commitHash)
	return nil
}
