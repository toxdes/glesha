package aws

import (
	"regexp"
	"strings"
)

type AwsValidator struct{}

func (a *AwsValidator) ValidateRegion(region string) bool {
	// https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html#AmazonS3-CreateBucket-request-LocationConstraint
	switch region {
	case "af-south-1",
		"ap-east-1",
		"ap-northeast-1",
		"ap-northeast-2",
		"ap-northeast-3",
		"ap-south-1",
		"ap-south-2",
		"ap-southeast-1",
		"ap-southeast-2",
		"ap-southeast-3",
		"ap-southeast-4",
		"ap-southeast-5",
		"ca-central-1",
		"cn-north-1",
		"cn-northwest-1",
		"EU",
		"eu-central-1",
		"eu-central-2",
		"eu-north-1",
		"eu-south-1",
		"eu-south-2",
		"eu-west-1",
		"eu-west-2",
		"eu-west-3",
		"il-central-1",
		"me-central-1",
		"me-south-1",
		"sa-east-1",
		"us-east-1",
		"us-east-2",
		"us-gov-east-1",
		"us-gov-west-1",
		"us-west-1",
		"us-west-2":
		return true
	default:
		return false
	}
}
func (a *AwsValidator) ValidateBucketName(bucketName string) bool {
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	if len(bucketName) < 3 || len(bucketName) > 63 {
		return false
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`).MatchString(bucketName) {
		return false
	}
	if strings.Contains(bucketName, "..") {
		return false
	}
	if regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`).MatchString(bucketName) {
		return false
	}
	if strings.HasPrefix(bucketName, "xn--") {
		return false
	}
	if strings.HasPrefix(bucketName, "sthree-") {
		return false
	}
	if strings.HasPrefix(bucketName, "amzn-s3-demo-") {
		return false
	}
	if strings.HasSuffix(bucketName, "-s3alias") {
		return false
	}
	if strings.HasSuffix(bucketName, "--ol-s3") {
		return false
	}
	if strings.HasSuffix(bucketName, ".mrap") {
		return false
	}
	if strings.HasSuffix(bucketName, "--x-s3") {
		return false
	}
	if strings.HasSuffix(bucketName, "--table-s3") {
		return false
	}
	if strings.Contains(bucketName, ".") {
		return false
	}
	return true
}
