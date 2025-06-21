package help_cmd

import "fmt"

var usageStr string = `
USAGE
    glesha help <command>

DESCRIPTION
    Prints usage information for a specified subcommand.

COMMANDS
    These are common glesha commands used in various situations -
        help       Help about a subcommand
        config     Help about config.json file
        add        Creates a glesha archive and upload task
        run        Runs a glesha task
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
	fmt.Print(usageStr)
}
