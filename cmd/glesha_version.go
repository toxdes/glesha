package cmd

import (
	"fmt"
	"os"
	"strings"
)

// will be populated at build time through -ldflags
var version string

func PrintVersion() {
	segments := strings.Split(os.Args[0], "/")
	fmt.Printf("%s - v%s\n", segments[len(segments)-1], version)
}
