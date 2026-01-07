package aws

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type AwsValidator struct{}

func (a *AwsValidator) ValidateRegion(region string) error {
	// https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html#AmazonS3-CreateBucket-request-LocationConstraint
	regions := []string{"af-south-1",
		"ap-east-1", "ap-northeast-1", "ap-northeast-2", "ap-northeast-3", "ap-south-1",
		"ap-south-2", "ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4",
		"ap-southeast-5", "ca-central-1", "cn-north-1", "cn-northwest-1", "EU",
		"eu-central-1", "eu-central-2", "eu-north-1", "eu-south-1", "eu-south-2", "eu-west-1",
		"eu-west-2", "eu-west-3", "il-central-1", "me-central-1", "me-south-1", "sa-east-1",
		"us-east-1", "us-east-2", "us-gov-east-1", "us-gov-west-1", "us-west-1", "us-west-2"}
	if !slices.Contains(regions, region) {
		return fmt.Errorf("aws: invalid region: %s", region)
	}
	return nil
}
func (a *AwsValidator) ValidateBucketName(bucketName string) error {
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	if len(bucketName) < 3 || len(bucketName) > 63 {
		return fmt.Errorf("aws: bucket name length should be between 3 and 62")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`).MatchString(bucketName) {
		return fmt.Errorf("aws: bucket name contains invalid characters")
	}
	if strings.Contains(bucketName, "..") {
		return fmt.Errorf("aws: bucket name should not contain consecutive periods")
	}
	if regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`).MatchString(bucketName) {
		return fmt.Errorf("aws: bucket name should not be an ip address")
	}
	if strings.HasPrefix(bucketName, "xn--") {
		return fmt.Errorf("aws: bucket names should not start with prefix xn--")
	}
	if strings.HasPrefix(bucketName, "sthree-") {
		return fmt.Errorf("aws: bucket names should not start with prefix sthree-")
	}
	if strings.HasPrefix(bucketName, "amzn-s3-demo-") {
		return fmt.Errorf("aws: bucket names should not start with prefix amzn-s3-demo-")
	}
	if strings.HasSuffix(bucketName, "-s3alias") {
		return fmt.Errorf("aws: bucket names should not end with suffix -s3alias")
	}
	if strings.HasSuffix(bucketName, "--ol-s3") {
		return fmt.Errorf("aws: bucket names should not end with suffix --ol-s3")
	}
	if strings.HasSuffix(bucketName, ".mrap") {
		return fmt.Errorf("aws: bucket names should not end with suffix .mrap")
	}
	if strings.HasSuffix(bucketName, "--x-s3") {
		return fmt.Errorf("aws: bucket names should not end with suffix --x-s3")
	}
	if strings.HasSuffix(bucketName, "--table-s3") {
		return fmt.Errorf("aws: bucket names should not end with suffix --table-s3")
	}
	// even though it's allowed to have a single period, but no two adjacent periods,
	// it's best to just avoid periods in the bucket name for now, until all edge cases
	// of what's accepted by S3 is figured out.
	if strings.Contains(bucketName, ".") {
		return fmt.Errorf("aws: bucket names should not contain periods (for now, they will be supported in a future release.)")
	}
	return nil
}

type AwsStorageClass string

// storage classes: https://docs.aws.amazon.com/AmazonS3/latest/userguide/storage-class-intro.html#sc-compare
const (
	AWS_SC_STANDARD            AwsStorageClass = "STANDARD"
	AWS_SC_INTELLIGENT_TIERING AwsStorageClass = "INTELLIGENT_TIERING"
	AWS_SC_STANDARD_IA         AwsStorageClass = "STANDARD_IA"
	AWS_SC_ONEZONE_IA          AwsStorageClass = "ONEZONE_IA"
	AWS_SC_GLACIER_IR          AwsStorageClass = "GLACIER_IR"
	AWS_SC_GLACIER             AwsStorageClass = "GLACIER"
	AWS_SC_DEEP_ARCHIVE        AwsStorageClass = "DEEP_ARCHIVE"
)

func GetAwsStorageClasses() []AwsStorageClass {
	return []AwsStorageClass{
		AWS_SC_STANDARD,
		AWS_SC_INTELLIGENT_TIERING,
		AWS_SC_STANDARD_IA,
		AWS_SC_ONEZONE_IA,
		AWS_SC_GLACIER_IR,
		AWS_SC_GLACIER,
		AWS_SC_DEEP_ARCHIVE,
	}
}

func (a *AwsValidator) ValidateStorageClass(storageClass string) error {
	storageClasses := GetAwsStorageClasses()
	if !slices.Contains(storageClasses, AwsStorageClass(storageClass)) {
		return fmt.Errorf("aws: invalid storage class %s", storageClass)
	}
	return nil
}

// returns a descriptive short label for each storage class
func GetStorageClassLabel(storageClass AwsStorageClass) string {
	switch storageClass {
	case AWS_SC_STANDARD:
		return "Standard"
	case AWS_SC_INTELLIGENT_TIERING:
		return "Intelligent Tiering (Auto)"
	case AWS_SC_STANDARD_IA:
		return "Standard (Infrequent Access)"
	case AWS_SC_GLACIER_IR:
		return "Glacier (Instant Retrieval)"
	case AWS_SC_GLACIER:
		return "Glacier (Flexible Retrieval)"
	case AWS_SC_DEEP_ARCHIVE:
		return "Glacier (Deep Archive)"
	default:
		return "unknown storage class"
	}
}

func (a *AwsValidator) ValidateAccountId(id uint64) error {
	length := 0
	// must be 12 digits
	for ; id > 0; id /= 10 {
		length += 1
	}
	if length != 12 {
		return fmt.Errorf("aws: account_id must have exactly 12 digits")
	}
	return nil
}
