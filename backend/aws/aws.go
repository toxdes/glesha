package aws

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"glesha/config"
	L "glesha/logger"
	"io"
	"net/http"
)

type AwsBackend struct {
	client     *http.Client
	bucketName string
	accessKey  string
	secretKey  string
	region     string
}

func NewAwsBackend() (*AwsBackend, error) {
	configs := config.Get()
	if configs.Aws == nil {
		return nil, fmt.Errorf("AWS: could not find aws configuration")
	}
	validator := AwsValidator{}
	if !validator.ValidateBucketName(configs.Aws.BucketName) {
		return nil, fmt.Errorf("AWS: bucket name is invalid")
	}
	if !validator.ValidateRegion(configs.Aws.Region) {
		return nil, fmt.Errorf("AWS: region is invalid")
	}
	L.Debug(fmt.Sprintf("config::ArchiveType %s", configs.ArchiveType))
	L.Debug(fmt.Sprintf("config::Aws::AccessKey %s", configs.Aws.AccessKey))
	L.Debug(fmt.Sprintf("config::Aws::BucketName %s", configs.Aws.BucketName))
	L.Debug(fmt.Sprintf("config::Aws::Region %s", configs.Aws.Region))
	client := &http.Client{}
	a := AwsBackend{
		client:     client,
		bucketName: configs.Aws.BucketName,
		accessKey:  configs.Aws.AccessKey,
		secretKey:  configs.Aws.SecretKey,
		region:     configs.Aws.Region,
	}
	return &a, nil
}

type AwsError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func (aws *AwsBackend) CreateResourceContainer() error {
	// TODO: handle 307 redirects if region is not us-east-1
	host := fmt.Sprintf("%s.s3.%s.amazonaws.com", aws.bucketName, aws.region)
	protocol := "https://"
	url := fmt.Sprintf("%s%s", protocol, host)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<CreateBucketConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
   <LocationConstraint>%s</LocationConstraint>
</CreateBucketConfiguration>`, aws.region)

	if aws.region == "us-east-1" {
		body = ""
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBufferString(body))
	req.Header.Set("host", host)
	req.Header.Set("content-type", "application/xml")
	req.Header.Set("x-amz-bucket-object-lock-enabled", "true")
	if err != nil {
		return err
	}

	err = aws.SignRequest(req)
	if err != nil {
		return err
	}
	resp, err := aws.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	L.Debug(L.HttpResponseString(resp))

	bodyBytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return err
	}

	if resp.StatusCode >= 200 && resp.StatusCode <= 300 {
		return nil
	}

	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	if resp.Header.Get("content-type") == "application/xml" {
		var awsError AwsError
		err := xml.Unmarshal(bodyBytes, &awsError)
		if err != nil {
			return err
		}
		if awsError.Code == "BucketAlreadyOwnedByYou" && resp.StatusCode == 409 {
			fmt.Printf("AWS: Bucket already exists: %s\n", aws.bucketName)
			return nil
		}
		if awsError.Code == "BucketAlreadyExists" && resp.StatusCode == 409 {
			return fmt.Errorf("AWS: Bucket name not available: %s (%s)", aws.bucketName, awsError.Message)
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("AWS: Cannot create bucket %s (%s)", aws.bucketName, awsError.Message)
		}
	}
	return nil
}

func (aws *AwsBackend) UploadResource(resourceFilePath string) error {
	return fmt.Errorf("AWS: UploadResource() not implmeneted")
}
