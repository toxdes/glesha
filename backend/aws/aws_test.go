package aws

import (
	"context"
	"fmt"
	"glesha/config"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	t.Run("NoAwsConfig", func(t *testing.T) {
		config.Get().Aws = nil
		_, err := new()
		assert.Error(t, err)
		assert.EqualError(t, err, "aws: could not find aws configuration")
	})

	t.Run("InvalidBucketName", func(t *testing.T) {
		config.Get().Aws = &config.Aws{
			BucketName: "invalid_bucket",
		}
		_, err := new()
		assert.Error(t, err)
		assert.EqualError(t, err, "aws: bucket name contains invalid characters")
	})

	t.Run("InvalidRegion", func(t *testing.T) {
		config.Get().Aws = &config.Aws{
			BucketName: "my-bucket",
			Region:     "invalid-region",
		}
		_, err := new()
		assert.Error(t, err)
		assert.EqualError(t, err, fmt.Sprintf("aws: invalid region: %s", config.Get().Aws.Region))
	})

	t.Run("InvalidStorageClass", func(t *testing.T) {
		config.Get().Aws = &config.Aws{
			BucketName:   "my-bucket",
			Region:       "us-east-1",
			StorageClass: "invalid-storage-class",
		}
		_, err := new()
		assert.Error(t, err)
		assert.EqualError(t, err, fmt.Sprintf("aws: invalid storage class %s", config.Get().Aws.StorageClass))
	})

	t.Run("InvalidAccountId", func(t *testing.T) {
		config.Get().Aws = &config.Aws{
			BucketName:   "my-bucket",
			Region:       "us-east-1",
			StorageClass: "STANDARD",
			AccountId:    12345,
		}
		_, err := new()
		assert.Error(t, err)
		assert.EqualError(t, err, "aws: account_id must have exactly 12 digits")
	})

	t.Run("ValidConfig", func(t *testing.T) {
		config.Get().Aws = &config.Aws{
			BucketName:   "my-bucket",
			Region:       "us-east-1",
			StorageClass: "STANDARD",
			AccountId:    123456789012,
			AccessKey:    "test-access-key",
			SecretKey:    "test-secret-key",
		}
		awsBackend, err := new()
		assert.NoError(t, err)
		assert.NotNil(t, awsBackend)
		assert.Equal(t, "my-bucket", awsBackend.bucketName)
	})
}

func TestAwsBackend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/success":
			w.WriteHeader(http.StatusOK)
		case "/already-owned":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>BucketAlreadyOwnedByYou</Code>
  <Message>Your previous request to create the named bucket succeeded and you already own it.</Message>
</Error>`)
		case "/already-exists":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>BucketAlreadyExists</Code>
  <Message>The requested bucket name is not available. The bucket namespace is shared by all users of the system. Please select a different name and try again.</Message>
</Error>`)
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>AccessDenied</Code>
  <Message>Access Denied</Message>
</Error>`)
		case "/test-key":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<InitiateMultipartUploadResult>
  <Bucket>test-bucket</Bucket>
  <Key>test-key</Key>
  <UploadId>test-upload-id</UploadId>
</InitiateMultipartUploadResult>`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	awsBackend := &AwsBackend{
		client:     server.Client(),
		bucketName: "test-bucket",
		region:     "us-east-1",
		protocol:   "http://",
		host:       server.Listener.Addr().String(),
	}

	t.Run("CreateResourceContainer_Success", func(t *testing.T) {
		awsBackend.host = server.Listener.Addr().String() + "/success"
		err := awsBackend.CreateResourceContainer(context.Background())
		assert.NoError(t, err)
	})

	t.Run("CreateResourceContainer_AlreadyOwned", func(t *testing.T) {
		awsBackend.host = server.Listener.Addr().String() + "/already-owned"
		err := awsBackend.CreateResourceContainer(context.Background())
		assert.NoError(t, err)
	})

	t.Run("CreateResourceContainer_AlreadyExists", func(t *testing.T) {
		awsBackend.host = server.Listener.Addr().String() + "/already-exists"
		err := awsBackend.CreateResourceContainer(context.Background())
		assert.Error(t, err)
	})

	t.Run("CreateResourceContainer_Forbidden", func(t *testing.T) {
		awsBackend.host = server.Listener.Addr().String() + "/forbidden"
		err := awsBackend.CreateResourceContainer(context.Background())
		assert.Error(t, err)
	})

	t.Run("CreateUploadResource_GetFileInfoError", func(t *testing.T) {
		_, err := awsBackend.CreateUploadResource(context.Background(), "test-key", "/non-existent-file")
		assert.Error(t, err)
	})

	t.Run("CreateUploadResource_NotReadableError", func(t *testing.T) {
		tempFile, err := os.CreateTemp("", "test-file")
		assert.NoError(t, err)
		defer os.Remove(tempFile.Name())

		err = os.Chmod(tempFile.Name(), 0000)
		assert.NoError(t, err)

		_, err = awsBackend.CreateUploadResource(context.Background(), "test-key", tempFile.Name())
		assert.Error(t, err)
	})

	t.Run("CreateUploadResource_Success", func(t *testing.T) {
		tempFile, err := os.CreateTemp("", "test-file")
		assert.NoError(t, err)
		defer os.Remove(tempFile.Name())

		awsBackend.host = server.Listener.Addr().String()
		result, err := awsBackend.CreateUploadResource(context.Background(), "test-key", tempFile.Name())
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result.Metadata.Json, "test-upload-id")
	})
}
