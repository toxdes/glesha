package aws

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"glesha/checksum"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
	"io"
	"net/http"
	"sync"
)

type CreateMultipartUploadResult struct {
	UploadId                string `json:"upload_id"`
	Key                     string `json:"key"`
	Bucket                  string `json:"bucket"`
	AwsChecksumAlgorithm    string `json:"aws_checksum_algorithm"`
	AwsChecksumType         string `json:"aws_checksum_type"`
	AwsServerSideEncryption string `json:"aws_server_side_encryption"`
}

func (aws *AwsBackend) createS3Bucket(ctx context.Context) error {
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

	req.Header.Set("Host", aws.host)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("x-amz-bucket-object-lock-enabled", "true")
	payloadHash := checksum.HexEncodeStr(checksum.Sha256([]byte(body)))
	err = aws.signRequest(req, payloadHash)
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
		// TODO: handle more errors
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

func (aws *AwsBackend) createMultipartUpload(
	ctx context.Context,
	taskKey string,
) (*CreateMultipartUploadResult, error) {
	url := fmt.Sprintf("%s%s/%s?uploads", aws.protocol, aws.host, taskKey)
	body := ""
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("aws: could not create new upload:%w", err)
	}
	req.Header.Set("Host", aws.host)
	req.Header.Set("Content-Type", "multipart/form-data")
	// TODO: maybe other values for cache-control make more sense here?
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("x-amz-storage-class", aws.storageClass)
	req.Header.Set("x-amz-expected-bucket-owner", fmt.Sprintf("%d", aws.accountId))
	req.Header.Set("x-amz-checksum-algorithm", "SHA256")
	req.Header.Set("x-amz-checksum-type", "COMPOSITE")

	err = aws.signRequest(req, AWS_UNSIGNED_PAYLOAD)

	if err != nil {
		return nil, fmt.Errorf("aws: could not sign CreateMultipartUpload request for task key %s: %w", taskKey, err)
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
		UploadId string   `xml:"UploadId"`
	}
	var uploadRes UploadResp
	err = xml.Unmarshal(bodyBytes, &uploadRes)
	if err != nil {
		return nil, fmt.Errorf("aws: failed to parse result of create multipart upload request: %w", err)
	}
	return &CreateMultipartUploadResult{
		UploadId:                uploadRes.UploadId,
		Bucket:                  uploadRes.Bucket,
		Key:                     uploadRes.Key,
		AwsChecksumAlgorithm:    resp.Header.Get("x-amz-checksum-algorithm"),
		AwsChecksumType:         resp.Header.Get("x-amz-checksum-type"),
		AwsServerSideEncryption: resp.Header.Get("x-amz-server-side-encryption"),
	}, nil
}

func (aws *AwsBackend) uploadBlock(
	ctx context.Context,
	uploadBlockRepo repository.UploadBlockRepository,
	upload *model.Upload,
	awsUploadRes *CreateMultipartUploadResult,
	taskKey string,
	blockId int64,
	workerId int,
	progress map[int][3]int64,
	totalSent *int64,
	mutex *sync.Mutex,
) error {

	L.Debug(fmt.Sprintf("Uploading block %d using worker %d", blockId, workerId))

	err := uploadBlockRepo.UpdateStatus(ctx, upload.Id, blockId, model.UB_STATUS_RUNNING)

	if err != nil {
		return fmt.Errorf("could not update status to %s for %d/%d:%w", model.UB_STATUS_RUNNING, upload.Id, blockId, err)
	}

	ub, err := uploadBlockRepo.GetById(ctx, blockId)
	if err != nil {
		return fmt.Errorf("could not find block with id %d for upload id %d:%w", blockId, upload.Id, err)
	}

	var blockContent = make([]byte, ub.Size)
	readCnt, err := file_io.ReadFromOffset(ctx, upload.FilePath, ub.FileOffset, blockContent)

	if err != nil && err != io.EOF {
		return fmt.Errorf("error while reading block %d for file %s: %w", blockId, upload.FilePath, err)
	}

	if readCnt > 0 {
		blockContentReader := bytes.NewReader(blockContent)
		// AWS::UploadPart request
		url := fmt.Sprintf(
			"%s%s/%s?partNumber=%d&uploadId=%s",
			aws.protocol,
			aws.host,
			taskKey,
			blockId,
			awsUploadRes.UploadId,
		)

		pr := file_io.ProgressReader{
			R:     blockContentReader,
			Total: ub.Size,
			Sent:  0,
			OnProgress: func(sent int64, total int64) {
				var p float64
				// process the progress update
				mutex.Lock()
				var np [3]int64
				np[0] = blockId
				delta := sent - progress[workerId][1]
				if delta < 0 {
					// worker is processing a new block now
					delta = sent
				}
				*totalSent += delta
				totalSentCopy := uint64(*totalSent)
				np[1] = sent
				np[2] = total
				progress[workerId] = np
				p = float64(*totalSent) * 100.0 / float64(upload.FileSize)
				progressCopy := make(map[int][3]int64, len(progress))
				for k, v := range progress {
					progressCopy[k] = v
				}

				mutex.Unlock()
				// print progress line
				workerProgress := aws.getProgressLine(progressCopy)
				L.Printf(
					"%s\r%sUploading: %.1f%% %s [%s Sent]%s\r%s%s",
					L.C_UP,
					L.C_CLEAR_LINE,
					p,
					L.ProgressBar(p, -1),
					L.HumanReadableBytes(totalSentCopy, 1),
					L.C_DOWN,
					L.C_CLEAR_LINE,
					workerProgress,
				)
			},
		}

		req, err := http.NewRequestWithContext(ctx, "PUT", url, &pr)
		if err != nil {
			return fmt.Errorf("could not create new PUT request for upload block with id %d:%w", blockId, err)
		}

		md5Sum := checksum.Md5(blockContent)
		sha256Sum := checksum.Sha256(blockContent)

		req.Header.Set("Host", aws.host)
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Content-MD5", checksum.Base64EncodeStr(md5Sum))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("x-amz-checksum-sha256", checksum.Base64EncodeStr(sha256Sum))
		req.Header.Set("x-amz-checksum-algorithm", "SHA256")
		req.Header.Set("x-amz-expected-bucket-owner", fmt.Sprintf("%d", aws.accountId))
		req.Header.Set("Content-Length", fmt.Sprintf("%d", readCnt))

		// NOTE: If the Content-Length header is missing or invalid, or if
		// the Transfer-Encoding is chunked, request.ContentLength will be set to -1.
		// This indicates that the content length is not explicitly known
		//  or is being handled by chunked encoding.
		// -> which is not currently supported by aws, so we explicitly set the content length
		req.ContentLength = readCnt

		err = aws.signRequest(req, checksum.HexEncodeStr(checksum.Sha256(blockContent)))
		if err != nil {
			return fmt.Errorf("aws: could not sign UploadPart request for block %d:%w", blockId, err)
		}

		resp, err := aws.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		L.Debug(fmt.Sprintf("\r%s%s", L.C_CLEAR_LINE, L.HttpResponseString(resp)))
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		var awsError AwsError
		err = xml.Unmarshal(bodyBytes, &awsError)
		if err == nil {
			// TODO: handle more errors
			if awsError.Code == "RequestTimeTooSkewed" && resp.StatusCode == 400 {
				return fmt.Errorf("aws: system clock is off by > 15 minutes, please sync system time with NTP")
			}
			if awsError.Code == "AccessDenied" && resp.StatusCode == 403 {
				return fmt.Errorf("aws: user lacks s3:CreateMultipartUpload permission")
			}

			if awsError.Code == "InvalidAccessKeyId" && resp.StatusCode == 403 {
				return fmt.Errorf("aws: access key is invalid, this is a potential bug")
			}
			if awsError.Code == "NoSuchBucket" && resp.StatusCode == 404 {
				return fmt.Errorf("aws: bucket %s does not exist in region: %s", aws.bucketName, aws.region)
			}
			if awsError.Code == "BucketRegionError" && resp.StatusCode == 409 {
				return fmt.Errorf("aws: bucket %s is in different region", aws.bucketName)
			}

			_, err = uploadBlockRepo.MarkError(ctx, upload.Id, blockId, awsError.Message)
			if err != nil {
				return fmt.Errorf("aws: could not mark upload as failed for block id %d of upload id %d: %w", blockId, ub.UploadId, err)
			}
			return fmt.Errorf("aws: unknown error: %s", awsError.Message)
		}
		L.Printf("\r%s", L.C_CLEAR_LINE)
		etag := resp.Header.Get("Etag")
		checksum := resp.Header.Get("X-Amz-Checksum-Sha256")

		err = uploadBlockRepo.MarkComplete(ctx, upload.Id, blockId, checksum, etag)
		if err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

type CompletedPart struct {
	PartNumber     int64  `xml:"PartNumber"`
	ETag           string `xml:"ETag"`
	ChecksumSHA256 string `xml:"ChecksumSHA256"`
}

type CompleteMultipartUpload struct {
	XMLName xml.Name        `xml:"CompleteMultipartUpload"`
	Xmlns   string          `xml:"xmlns,attr"`
	Parts   []CompletedPart `xml:"Part"`
}

func (aws *AwsBackend) completeMultipartUpload(
	ctx context.Context,
	uploadRepo repository.UploadRepository,
	uploadBlockRepo repository.UploadBlockRepository,
	upload *model.Upload,
	uploadRes *CreateMultipartUploadResult,
	task *model.Task,
) error {
	blocks, err := uploadBlockRepo.GetCompletedBlocksForUploadId(ctx, upload.Id)
	if err != nil {
		return fmt.Errorf("could not get blocks for upload id %d", upload.Id)
	}
	if len(blocks) != int(upload.TotalBlocks) {
		return fmt.Errorf("not all blocks are completed, cannot complete multipart upload")
	}
	p := CompleteMultipartUpload{}
	p.Xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"
	var concatenatedHashBytes bytes.Buffer
	for _, b := range blocks {
		p.Parts = append(
			p.Parts,
			CompletedPart{
				PartNumber:     b.Id,
				ETag:           b.Etag,
				ChecksumSHA256: b.Checksum,
			})
		rawChecksum, err := checksum.Base64DecodeStr(b.Checksum)
		if err != nil {
			return fmt.Errorf("could not decode checksum for block id %d of upload id %d: %w", b.Id, b.UploadId, err)
		}
		concatenatedHashBytes.Write(rawChecksum)
	}

	sum := checksum.Base64EncodeStr(checksum.Sha256(concatenatedHashBytes.Bytes()))
	checksumHeaderVal := fmt.Sprintf("%s-%d", sum, len(p.Parts))

	// aws::CompleteMultipartUpload request
	url := fmt.Sprintf("%s%s/%s?uploadId=%s",
		aws.protocol,
		aws.host,
		task.Key(),
		uploadRes.UploadId,
	)

	content, err := xml.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("could not construct body for aws::CompleteMultipartUpload :%w", err)
	}
	body := []byte(fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n%s", string(content)))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("could not create aws::CompleteMultipartUpload request: %w", err)
	}

	req.Header.Set("Host", aws.host)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("x-amz-expected-bucket-owner", fmt.Sprintf("%d", aws.accountId))
	req.Header.Set("x-amz-mp-object-size", fmt.Sprintf("%d", upload.FileSize))
	req.Header.Set("x-amz-checksum-sha256", checksumHeaderVal)
	req.Header.Set("x-amz-checksum-algorithm", "SHA256")
	req.Header.Set("x-amz-checksum-type", "COMPOSITE")

	payloadHash := checksum.HexEncodeStr(checksum.Sha256(body))
	err = aws.signRequest(req, payloadHash)
	if err != nil {
		return fmt.Errorf("could not sign aws::CompleteMultipartUpload request: %w", err)
	}

	L.Info("Completing AWS upload")
	resp, err := aws.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	L.Debug(fmt.Sprintf("\r%s%s", L.C_CLEAR_LINE, L.HttpResponseString(resp)))
	bodyBytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return fmt.Errorf("could not read response body of aws::CompleteMultipartUpload request")
	}
	var awsError AwsError
	err = xml.Unmarshal(bodyBytes, &awsError)
	if err == nil {
		if awsError.Code == "RequestTimeTooSkewed" && resp.StatusCode == 400 {
			return fmt.Errorf("aws: system clock is off by > 15 minutes, please sync system time with NTP")
		}
		if awsError.Code == "AccessDenied" && resp.StatusCode == 403 {
			return fmt.Errorf("aws: user lacks s3:CreateMultipartUpload permission")
		}

		if awsError.Code == "InvalidAccessKeyId" && resp.StatusCode == 403 {
			return fmt.Errorf("aws: access key is invalid, this is a potential bug")
		}
		if awsError.Code == "NoSuchBucket" && resp.StatusCode == 404 {
			return fmt.Errorf("aws: bucket %s does not exist in region: %s", aws.bucketName, aws.region)
		}
		if awsError.Code == "BucketRegionError" && resp.StatusCode == 409 {
			return fmt.Errorf("aws: bucket %s is in different region", aws.bucketName)
		}
		return fmt.Errorf("aws: unknown error: %s", awsError.Message)
	}

	type CompleteUploadResp struct {
		XMLName        xml.Name `xml:"CompleteMultipartUploadResult"`
		Location       string   `xml:"Location"`
		Bucket         string   `xml:"Bucket"`
		Key            string   `xml:"Key"`
		ChecksumSHA256 string   `xml:"ChecksumSHA256"`
		ETag           string   `xml:"ETag"`
	}

	var completeUploadRes CompleteUploadResp
	err = xml.Unmarshal(bodyBytes, &completeUploadRes)
	if err != nil {
		return fmt.Errorf("could not parse aws::CompleteMultipartUpload response for upload id %d:%w", upload.Id, err)
	}
	err = uploadRepo.MarkComplete(ctx, upload.Id, completeUploadRes.Location)

	if err != nil {
		return err
	}

	return nil
}
