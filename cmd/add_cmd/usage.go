package add_cmd

import L "glesha/logger"

const usageStr string = `
USAGE
glesha add [OPTIONS] PATH

DESCRIPTION
Queues a glesha task that -
1. Archives the given directory into the specified archive format
2. Uploads the generated archive to the specified storage provider
The task does not start automatically. See 'glesha help run' for details.

OPTIONS
--provider, -p [PROVIDER]
Specifies which cloud storage providers to use for uploading.
This argument, if provided, takes preference over provider specified
in the CONFIG.
CONFIG must have relevant credentials to facilitate an upload
for the specified providers.
Supported values for PROVIDER: aws

--archive-format, -a [ARCHIVE_FORMAT]
Specifies which archive format to use for archiving.
This argument, if provided, takes precedence over archive_format
specified in the CONFIG.
Supported values for ARCHIVE_FORMAT: targz

--output, -o
Path to directory where archive should be generated
Default is: ~/.glesha-cache

--config, -c
Path to config.json file
Default is: ~/.config/glesha/config.json
Use "glesha help config" for more information on configuring glesha.

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
3. never:   never use colored output

PATH
Directory path that should be archived

EXAMPLES
1. Create a targz archive and upload to s3-glacier, assuming
'aws_config.json' contains the required credentials.
glesha add -c ~/.config/glesha/aws_config.json ./dir_to_upload

2. Create a zip archive and upload to google_drive.
glesha add -a zip -c ~/.config/glesha/gd_config.json ./dir_to_upload

SEE ALSO
1. glesha help run
`

func Usage() string {
	return usageStr
}

func PrintUsage() {
	L.Print(usageStr)
}
