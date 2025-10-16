package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAwsValidator_ValidateRegion(t *testing.T) {
	validator := AwsValidator{}

	t.Run("ValidRegions", func(t *testing.T) {
		validRegions := []string{"us-east-1", "ap-south-1", "eu-west-2"}
		for _, region := range validRegions {
			assert.NoError(t, validator.ValidateRegion(region), "Expected region %s to be valid", region)
		}
	})

	t.Run("InvalidRegions", func(t *testing.T) {
		invalidRegions := []string{"us-east-3", "invalid-region", ""}
		for _, region := range invalidRegions {
			assert.Error(t, validator.ValidateRegion(region), "Expected region %s to be invalid", region)
		}
	})
}

func TestAwsValidator_ValidateBucketName(t *testing.T) {
	validator := AwsValidator{}

	t.Run("ValidBucketNames", func(t *testing.T) {
		validNames := []string{"my-bucket", "123bucket"}
		for _, name := range validNames {
			assert.NoError(t, validator.ValidateBucketName(name), "Expected bucket name %s to be valid", name)
		}
	})

	t.Run("InvalidBucketNames", func(t *testing.T) {
		invalidNames := []string{
			"my..bucket",
			"my.bucket1", // even though it's a valid name, I deem it invalid until there's a good case for it
			"my_bucket",
			"-my-bucket",
			"my-bucket-",
			"xn--my-bucket",
			"my-bucket-s3alias",
			"192.168.1.1",
		}
		for _, name := range invalidNames {
			assert.Error(t, validator.ValidateBucketName(name), "Expected bucket name %s to be invalid", name)
		}
	})
}

func TestAwsValidator_ValidateStorageClass(t *testing.T) {
	validator := AwsValidator{}

	t.Run("ValidStorageClasses", func(t *testing.T) {
		validClasses := []string{"STANDARD", "GLACIER", "DEEP_ARCHIVE"}
		for _, class := range validClasses {
			assert.NoError(t, validator.ValidateStorageClass(class), "Expected storage class %s to be valid", class)
		}
	})

	t.Run("InvalidStorageClasses", func(t *testing.T) {
		invalidClasses := []string{"STANDARD_IAX", "", "glacier"}
		for _, class := range invalidClasses {
			assert.Error(t, validator.ValidateStorageClass(class), "Expected storage class %s to be invalid", class)
		}
	})
}

func TestAwsValidator_ValidateAccountId(t *testing.T) {
	validator := AwsValidator{}

	t.Run("ValidAccountIds", func(t *testing.T) {
		validIds := []uint64{123456789012, 987654321098}
		for _, id := range validIds {
			assert.NoError(t, validator.ValidateAccountId(id), "Expected account Id %d to be valid", id)
		}
	})

	t.Run("InvalidAccountIds", func(t *testing.T) {
		invalidIds := []uint64{12345, 1234567890123}
		for _, id := range invalidIds {
			assert.Error(t, validator.ValidateAccountId(id), "Expected account Id %d to be invalid", id)
		}
	})
}
