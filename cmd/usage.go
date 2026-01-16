package cmd

import L "glesha/logger"

var usageStr string = `
USAGE
glesha [-v | -version] [-h | -help] <command> [<args>]

DESCRIPTION
glesha is a cross-platform archive and upload utility.

COMMANDS
These are common glesha commands used in various situations -
help       Help about a subcommand
add        Creates a glesha archive and upload task
run        Runs a glesha task
tui        Interactive terminal user interface
ls         Lists all available glesha tasks
rm         Deletes a glesha task, and relevant cache files
cleanup    Cleans up cache, unwanted files created by glesha.

EXAMPLES
See 'glesha help <command>' to read about a specific subcommand.

SEE ALSO
1. glesha help add
2. glesha help ls
`

func Usage() string {
	return usageStr
}

func PrintUsage() {
	L.Print(usageStr)
}
