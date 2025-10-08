package aws

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	L "glesha/logger"
	"io"
	"net/http"
)

type CreateMultipartUploadResult struct {
	UploadID                string `json:"upload_id"`
	Key                     string `json:"key"`
	Bucket                  string `json:"bucket"`
	AwsChecksumAlgorithm    string `json:"aws_checksum_algorithm"`
	AwsChecksumType         string `json:"aws_checksum_type"`
	AwsServerSideEncryption string `json:"aws_server_side_encryption"`
}

func (aws *AwsBackend) CreateMultipartUpload(ctx context.Context, taskKey string) (*CreateMultipartUploadResult, error) {
	url := fmt.Sprintf("%s%s/%s?uploads", aws.protocol, aws.host, taskKey)
	body := ""
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("aws: could not create new upload:%w", err)
	}
	req.Header.Set("host", aws.host)
	req.Header.Set("content-type", "multipart/form-data")
	// TODO: maybe other values for cache-control make more sense here?
	req.Header.Set("cache-control", "no-cache")
	req.Header.Set("x-amz-storage-class", aws.storageClass)
	req.Header.Set("x-amz-expected-bucket-owner", fmt.Sprintf("%d", aws.accountID))
	req.Header.Set("x-amz-checksum-algorithm", "SHA256")
	req.Header.Set("x-amz-checksum-type", "COMPOSITE")

	err = aws.SignRequest(ctx, req, false)

	if err != nil {
		return nil, fmt.Errorf("aws: could not sign request: %w", err)
	}
	L.Info("Creating AWS Multipart Upload")
	resp, err := aws.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	L.Debug(L.HttpResponseString(resp))

	bodyBytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var awsError AwsError
	err = xml.Unmarshal(bodyBytes, &awsError)
	if err == nil {
		if awsError.Code == "RequestTimeTooSkewed" && resp.StatusCode == 400 {
			return nil, fmt.Errorf("aws: system clock is off by > 15 minutes, please sync system time with NTP")
		}
		if awsError.Code == "AccessDenied" && resp.StatusCode == 403 {
			return nil, fmt.Errorf("aws: user lacks s3:CreateMultipartUpload permission")
		}

		if awsError.Code == "InvalidAccessKeyId" && resp.StatusCode == 403 {
			return nil, fmt.Errorf("aws: access key is invalid, this is a potential bug")
		}
		if awsError.Code == "NoSuchBucket" && resp.StatusCode == 404 {
			return nil, fmt.Errorf("aws: bucket %s does not exist in region: %s", aws.bucketName, aws.region)
		}
		if awsError.Code == "BucketRegionError" && resp.StatusCode == 409 {
			return nil, fmt.Errorf("aws: bucket %s is in different region", aws.bucketName)
		}
		return nil, fmt.Errorf("aws: unknown error: %s", awsError.Message)
	}
	type UploadResp struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}
	var uploadRes UploadResp
	err = xml.Unmarshal(bodyBytes, &uploadRes)
	if err != nil {
		return nil, fmt.Errorf("aws: failed to parse result of create multipart upload request: %w", err)
	}
	return &CreateMultipartUploadResult{
		UploadID:                uploadRes.UploadID,
		Bucket:                  uploadRes.Bucket,
		Key:                     uploadRes.Key,
		AwsChecksumAlgorithm:    resp.Header.Get("x-amz-checksum-algorithm"),
		AwsChecksumType:         resp.Header.Get("x-amz-checksum-type"),
		AwsServerSideEncryption: resp.Header.Get("x-amz-server-side-encryption"),
	}, nil
}
