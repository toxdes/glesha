package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"glesha/backend"
	"glesha/config"
	"glesha/file_io"
	L "glesha/logger"
	"io"
	"net/http"
	"strings"
)

type AwsBackend struct {
	client       *http.Client
	bucketName   string
	accessKey    string
	secretKey    string
	accountID    uint64
	region       string
	storageClass string
	host         string
	protocol     string
}

const STORAGE_BACKEND_METADATA_SCHEMA_VERSION int64 = 1

func new() (*AwsBackend, error) {
	configs := config.Get()
	if configs.Aws == nil {
		return nil, fmt.Errorf("aws: could not find aws configuration")
	}
	validator := AwsValidator{}
	if !validator.ValidateBucketName(configs.Aws.BucketName) {
		return nil, fmt.Errorf("aws: bucket name is invalid")
	}
	if !validator.ValidateRegion(configs.Aws.Region) {
		return nil, fmt.Errorf("aws: region is invalid")
	}
	if !validator.ValidateStorageClass(configs.Aws.StorageClass) {
		return nil, fmt.Errorf("aws: storage class is invalid")
	}
	if !validator.ValidateAccountID(configs.Aws.AccountID) {
		return nil, fmt.Errorf("aws: account id is invalid")
	}
	L.Debug(fmt.Sprintf("config::ArchiveFormat %s", configs.ArchiveFormat))
	L.Debug(fmt.Sprintf("config::Aws::BucketName %s", configs.Aws.BucketName))
	L.Debug(fmt.Sprintf("config::Aws::Region %s", configs.Aws.Region))
	L.Debug(fmt.Sprintf("config::Aws::StorageClass %s", configs.Aws.StorageClass))
	client := &http.Client{}
	host := fmt.Sprintf("%s.s3.%s.amazonaws.com", configs.Aws.BucketName, configs.Aws.Region)
	protocol := "https://"
	a := AwsBackend{
		client:       client,
		bucketName:   configs.Aws.BucketName,
		accessKey:    configs.Aws.AccessKey,
		secretKey:    configs.Aws.SecretKey,
		region:       configs.Aws.Region,
		storageClass: configs.Aws.StorageClass,
		accountID:    configs.Aws.AccountID,
		host:         host,
		protocol:     protocol,
	}
	return &a, nil
}

type AWSFactory struct{}

func (af *AWSFactory) NewStorageBackend() (backend.StorageBackend, error) {
	return new()
}

type AwsError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func (aws *AwsBackend) CreateResourceContainer(ctx context.Context) error {
	// TODO: handle 307 redirects if region is not us-east-1
	url := fmt.Sprintf("%s%s", aws.protocol, aws.host)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<CreateBucketConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
   <LocationConstraint>%s</LocationConstraint>
</CreateBucketConfiguration>`, aws.region)

	if aws.region == "us-east-1" {
		body = ""
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBufferString(body))

	if err != nil {
		return fmt.Errorf("aws: could not create storage bucket: %w", err)
	}

	req.Header.Set("host", aws.host)
	req.Header.Set("content-type", "application/xml")
	req.Header.Set("x-amz-bucket-object-lock-enabled", "true")

	err = aws.SignRequest(ctx, req, true)
	if err != nil {
		return err
	}
	L.Info(fmt.Sprintf("Creating Aws bucket: %s", aws.bucketName))
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
			L.Printf("aws: Bucket already exists: %s\n", aws.bucketName)
			return nil
		}
		if awsError.Code == "BucketAlreadyExists" && resp.StatusCode == 409 {
			return fmt.Errorf("aws: Bucket name not available: %s (%s)", aws.bucketName, awsError.Message)
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("aws: Cannot create bucket %s (%s)", aws.bucketName, awsError.Message)
		}
	}
	return nil
}

func (aws *AwsBackend) CreateUploadResource(
	ctx context.Context,
	taskKey string,
	resourceFilePath string,
) (*backend.CreateUploadResult, error) {
	resourceFileInfo, err := file_io.GetFileInfo(resourceFilePath)
	if err != nil {
		return nil, err
	}
	L.Info(fmt.Sprintf("aws: initiating upload: %s (%s)",
		resourceFilePath,
		L.HumanReadableBytes(resourceFileInfo.Size)))

	cost, err := aws.EstimateCost(ctx, resourceFileInfo.Size, "INR")
	if err != nil {
		return nil, err
	}
	L.Info("aws: Estimating costs")
	L.Print(cost)
	if !file_io.IsReadable(resourceFilePath) {
		return nil, fmt.Errorf("could not read resource: %s", resourceFilePath)
	}

	uploadRes, err := aws.CreateMultipartUpload(ctx, taskKey)
	if err != nil {
		return nil, fmt.Errorf("aws: could not create multipart upload: %w", err)
	}
	uploadResJson, err := json.Marshal(uploadRes)
	if err != nil {
		return nil, fmt.Errorf("aws: could not parse response from CreateMultipartUpload: %w", err)
	}
	metadata := backend.StorageMetadata{
		Json:          string(uploadResJson),
		SchemaVersion: STORAGE_BACKEND_METADATA_SCHEMA_VERSION,
	}

	info, err := file_io.GetFileInfo(resourceFilePath)
	if err != nil {
		return nil, err
	}

	return &backend.CreateUploadResult{
		Metadata:         metadata,
		BlockSizeInBytes: aws.GetOptimalBlockSizeForSize(int64(info.Size)),
	}, nil
}

func getExchangeRate(c1 string, c2 string) (float64, error) {

	if c1 == "USD" && c2 == "INR" {
		return float64(85.56), nil
	}

	return -1, fmt.Errorf("getExchangeRate() does not support: %s-%s rate yet", c1, c2)
}

func (aws *AwsBackend) EstimateCost(ctx context.Context, size uint64, currency string) (string, error) {
	exchangeRate, err := getExchangeRate("USD", currency)
	if err != nil {
		return "", err
	}
	awsStorageCostPerYear := map[string]float64{
		"StandardFrequent":   12 * float64(size) * float64(0.023) * exchangeRate * float64(1e-9),
		"StandardInfrequent": 12 * float64(size) * float64(0.0125) * exchangeRate * float64(1e-9),
		"Express":            12 * float64(size) * float64(0.11) * exchangeRate * float64(1e-9),
		"GlacierFlexible":    12 * float64(size) * float64(0.0037) * exchangeRate * float64(1e-9),
		"GlacierDeepArchive": 12 * float64(size) * float64(0.00099) * exchangeRate * float64(1e-9),
	}

	var sb strings.Builder
	headerLine := fmt.Sprintf("    S3 Storage Class               Cost for %s (per year)", L.HumanReadableBytes(size))
	sb.WriteString(fmt.Sprintf("%s\n", L.Line(len(headerLine))))
	sb.WriteString(headerLine)
	sb.WriteString(fmt.Sprintf("\n%s\n", L.Line(len(headerLine))))
	sb.WriteString(fmt.Sprintf("Standard (Frequent Retrieval)   :   %*.2f %s\n", 10, awsStorageCostPerYear["StandardFrequent"], currency))
	sb.WriteString(fmt.Sprintf("Standard (Infrequent Retrieval) :   %*.2f %s\n", 10, awsStorageCostPerYear["StandardInfrequent"], currency))
	sb.WriteString(fmt.Sprintf("Express (High Performance)      :   %*.2f %s\n", 10, awsStorageCostPerYear["Express"], currency))
	sb.WriteString(fmt.Sprintf("Glacier (Flexible Retrieval)    :   %*.2f %s\n", 10, awsStorageCostPerYear["GlacierFlexible"], currency))
	sb.WriteString(fmt.Sprintf("Glacier (Deep Archive)          :   %*.2f %s", 10, awsStorageCostPerYear["GlacierDeepArchive"], currency))
	sb.WriteString(fmt.Sprintf("\n%s\n", L.Line(len(headerLine))))
	sb.WriteString("Note: Above storage costs are an approximation based on storage costs for us-east-1 region, it does not include retrieval/deletion costs.\n")
	return sb.String(), nil
}

func (a *AwsBackend) UploadResource(ctx context.Context, uploadID int64) error {
	return fmt.Errorf("aws: UploadResource not implemented yet")
}

func (aws *AwsBackend) GetOptimalBlockSizeForSize(sizeInBytes int64) int64 {
	const MB int64 = 1024 * 1024
	const GB int64 = 1024 * MB
	if sizeInBytes <= 20*MB {
		return 10 * MB
	}
	if sizeInBytes <= 5*GB {
		return 50 * MB
	}
	if sizeInBytes <= 20*GB {
		return 100 * MB
	}
	// TODO: tweat these parameters for costs/efficiency etc after profiling
	// since 1e4 is the max limit for number of parts
	// max upload size for a single file, is limited to 1.5 TB
	return 150 * MB
}

func (aws *AwsBackend) IsBlockSizeOK(blockSize int64, fileSize int64) error {
	if blockSize == 0 {
		return fmt.Errorf("aws: block_size cannot be zero")
	}
	parts := (fileSize + blockSize - 1) / blockSize
	if parts > 10000 {
		return fmt.Errorf("aws: block_size is too small")
	}
	return nil
}
