package run_cmd

import L "glesha/logger"

const usageStr string = `
USAGE
glesha run [OPTIONS] ID

DESCRIPTION
Runs an existing glesha task with <ID> -
1. Archives the given directory into the specified archive format
2. Uploads the generated archive to the specified storage provider

OPTIONS
--jobs, -j <jobs>
Specify maximum number of jobs to run simultaneously.
Defaults to 1 if not specified.

--log-level, -L <log-level>
Specify log output level
Default: debug
Accepted values (in order of increasing amount of output) -
debug, info, warn, error, silent

--color <color-mode>
Specify output color mode.
Default: auto
Accepted values: auto, always, never
1. auto:    automatically determine if colors are supported
2. always:  always use colored output
3. never:   use only 1 color

ID
ID of the task that you want to run.

EXAMPLES
1. Run a task with debug logs and use at most 6 jobs simultaneously -
glesha run -j 6 -L debug 2039

2. Run a task with 1 job -
glesha run 2039

SEE ALSO
1. glesha help run
`

func Usage() string {
	return usageStr
}

func PrintUsage() {
	L.Print(usageStr)
}
