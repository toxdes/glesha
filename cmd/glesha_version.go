package cmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// will be populated at build time through -ldflags
var version string

func PrintVersion() {
	name := filepath.Base(os.Args[0])
	fmt.Printf("%s - v%s\n", name, version)
}
