package aws

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"glesha/config"
	"glesha/file_io"
	L "glesha/logger"
	"io"
	"net/http"
	"strings"
)

type AwsBackend struct {
	client     *http.Client
	bucketName string
	accessKey  string
	secretKey  string
	region     string
	host       string
	protocol   string
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
	L.Debug(fmt.Sprintf("config::ArchiveFormat %s", configs.ArchiveFormat))
	L.Debug(fmt.Sprintf("config::Aws::BucketName %s", configs.Aws.BucketName))
	L.Debug(fmt.Sprintf("config::Aws::Region %s", configs.Aws.Region))
	client := &http.Client{}
	host := fmt.Sprintf("%s.s3.%s.amazonaws.com", configs.Aws.BucketName, configs.Aws.Region)
	protocol := "https://"
	a := AwsBackend{
		client:     client,
		bucketName: configs.Aws.BucketName,
		accessKey:  configs.Aws.AccessKey,
		secretKey:  configs.Aws.SecretKey,
		region:     configs.Aws.Region,
		host:       host,
		protocol:   protocol,
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
	url := fmt.Sprintf("%s%s", aws.protocol, aws.host)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<CreateBucketConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
   <LocationConstraint>%s</LocationConstraint>
</CreateBucketConfiguration>`, aws.region)

	if aws.region == "us-east-1" {
		body = ""
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBufferString(body))
	req.Header.Set("host", aws.host)
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
	size, err := file_io.FileSizeInBytes(resourceFilePath)
	if err != nil {
		return err
	}
	fmt.Printf("Aws: initiating upload: %s (%s)\n", resourceFilePath, L.HumanReadableBytes(size))

	cost, err := aws.EstimateCost(size, "INR")
	if err != nil {
		return err
	}
	fmt.Println("AWS: Estimating costs...")
	fmt.Print(cost)
	if !file_io.IsReadable(resourceFilePath) {
		return fmt.Errorf("Cannot read resource: %s", resourceFilePath)
	}

	return fmt.Errorf("AWS: upload not implemented yet")
}

func getExchangeRate(c1 string, c2 string) (float64, error) {

	if c1 == "USD" && c2 == "INR" {
		return float64(85.56), nil
	}

	return -1, fmt.Errorf("getExchangeRate() does not support: %s-%s rate yet", c1, c2)
}

func (aws *AwsBackend) EstimateCost(size uint64, currency string) (string, error) {
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
	sb.WriteString(fmt.Sprintf("    S3 Storage Class               Cost for %s (per year)\n", L.HumanReadableBytes(size)))
	sb.WriteString(fmt.Sprintf("Standard (Frequent Retrieval)   :   %*.2f %s\n", 10, awsStorageCostPerYear["StandardFrequent"], currency))
	sb.WriteString(fmt.Sprintf("Standard (Infrequent Retrieval) :   %*.2f %s\n", 10, awsStorageCostPerYear["StandardInfrequent"], currency))
	sb.WriteString(fmt.Sprintf("Express (High Performance)      :   %*.2f %s\n", 10, awsStorageCostPerYear["Express"], currency))
	sb.WriteString(fmt.Sprintf("Glacier (Flexible Retrieval)    :   %*.2f %s\n", 10, awsStorageCostPerYear["GlacierFlexible"], currency))
	sb.WriteString(fmt.Sprintf("Glacier (Deep Archive)          :   %*.2f %s\n", 10, awsStorageCostPerYear["GlacierDeepArchive"], currency))
	sb.WriteString("Note: Above storage costs are an approximation based on storage costs for us-east-1 region, it does not include retrieval/deletion costs.\n")
	return sb.String(), nil
}
