package help_cmd

import "fmt"

const configUsageStr string = `
CONFIGURATION
    Configuration file is a JSON file used for storing storage provider credentials
    and preferences. You could have different config.json files for different usecases.

    Some parts of the configuration file can be overriden from CLI arguments.
    When you first run the program, a default config will be created for you at
    '~/.config/glesha/config.json', and you are supposed to modify this configuration
    to add credentials.

SAMPLE CONFIG

        {
            "archive_format": "targz",
            "provider": "aws",
            "aws": {
                "access_key": "YOUR_ACCESS_KEY",
                "secret_key": "YOUR_SECRET_KEY",
                "region": "ap-south-1",
                "bucket_name": "glesha-backup"
            }
        }

OPTIONS
    archive_format
        Specifies which archive format to use for archiving.
        This option is equivalent to --archive-format argument.
        Supported values for ARCHIVE_FORMAT: targz

    provider
        Specifies which storage provider to use for uploading.
        This option is equivalent to --provider argument.
        Supported values for PROVIDER: aws

    aws.access_key, aws.secret_key
        Credentials for an aws account that has full access to S3.
        These credentials are private, and should not be exposed. 

    aws.region
        Region to which the archive will be uploaded.

    aws.bucket_name
        AWS S3 bucket names need to be globally unique across all aws users.
        so, choose a globally unique name for your S3 bucket.
`

func ConfigUsage() string {
	return configUsageStr
}

func ConfigPrintUsage() {
	fmt.Print(configUsageStr)
}
