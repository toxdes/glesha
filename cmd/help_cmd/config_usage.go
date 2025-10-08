package help_cmd

import (
	L "glesha/logger"
)

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
                "account_id": YOUR_12_DIGIT_ACCOUNT_ID,
                "access_key": "YOUR_ACCESS_KEY",
                "secret_key": "YOUR_SECRET_KEY",
                "region": "ap-south-1",
                "bucket_name": "glesha-backup",
                "storage_class": "STANDARD_IA"
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

    aws.account_id 
        12-digit AWS account ID, used to identify for ownership
        of the S3 bucket, to prevent accidental modifications.

    aws.access_key, aws.secret_key
        Credentials for an aws account that has full access to S3.
        These credentials are private, and should not be exposed. 

    aws.region
        Region to which the archive will be uploaded.

    aws.bucket_name
        AWS S3 bucket names need to be globally unique across all aws users.
        so, choose a globally unique name for your S3 bucket.
    
    aws.storage_class
        AWS S3 storage class to be used by default. 
        Supported values for storage_class(approx sorted by storage costs)
        (high to low): STANDARD, INTELLIGENT_TIERING, STANDARD_IA,
        ONEZONE_IA,GLACIER_IR, GLACIER, DEEP_ARCHIVE
        For more info: https://v.gd/s3_storage_classes

`

func ConfigUsage() string {
	return configUsageStr
}

func ConfigPrintUsage() {
	L.Print(configUsageStr)
}
