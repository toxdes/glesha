package run_cmd

import "fmt"

const usageStr string = `
USAGE
    glesha run [OPTIONS] ID

DESCRIPTION
    Runs an existing glesha task with <ID> - 
        1. Archives the given directory into the specified archive format
        2. Uploads the generated archive to the specified storage provider

OPTIONS
    -L <log-level>
        Control output logs level, default log level is 'error'.
        Accepted values: debug, info, warn, error

ID
    ID of the task that you want to run.

EXAMPLES
    1. Run a task with debug logs -
        glesha run -L debug 2039 

    2. Run a task - 
        glesha run 2039

SEE ALSO
    1. glesha help run
`

func Usage() string {
	return usageStr
}

func PrintUsage() {
	fmt.Print(usageStr)
}
