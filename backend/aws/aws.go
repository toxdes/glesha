package aws

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"glesha/backend"
	"glesha/config"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type AwsBackend struct {
	client       *http.Client
	bucketName   string
	accessKey    string
	secretKey    string
	accountId    uint64
	region       string
	storageClass string
	host         string
	protocol     string
}

type AwsError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

type AWSFactory struct{}

const STORAGE_BACKEND_METADATA_SCHEMA_VERSION int64 = 1
const AWS_UNSIGNED_PAYLOAD = "UNSIGNED-PAYLOAD"

func new() (*AwsBackend, error) {
	configs := config.Get()
	if configs.Aws == nil {
		return nil, fmt.Errorf("aws: could not find aws configuration")
	}
	validator := AwsValidator{}
	err := validator.ValidateBucketName(configs.Aws.BucketName)
	if err != nil {
		return nil, err
	}

	err = validator.ValidateRegion(configs.Aws.Region)
	if err != nil {
		return nil, err
	}

	err = validator.ValidateStorageClass(configs.Aws.StorageClass)

	if err != nil {
		return nil, err
	}

	err = validator.ValidateAccountId(configs.Aws.AccountId)

	if err != nil {
		return nil, err
	}

	L.Debug("aws: config is valid")
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
		accountId:    configs.Aws.AccountId,
		host:         host,
		protocol:     protocol,
	}
	return &a, nil
}

func (af *AWSFactory) NewStorageBackend() (backend.StorageBackend, error) {
	return new()
}

func (aws *AwsBackend) IsBlockSizeOK(blockSize int64, fileSize int64) error {
	// AWS::MultipartUpload only supports parts between 1-10_000
	if blockSize == 0 {
		return fmt.Errorf("aws: block_size cannot be zero")
	}
	parts := (fileSize + blockSize - 1) / blockSize
	if parts > 10_000 {
		return fmt.Errorf("aws: block_size is too small")
	}
	return nil
}

func (aws *AwsBackend) CreateResourceContainer(ctx context.Context) error {
	return aws.createS3Bucket(ctx)
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
	L.Printf("Initiating aws upload: %s (%s)\n",
		resourceFilePath,
		L.HumanReadableBytes(resourceFileInfo.Size, 2))

	cost, err := aws.estimateCost(ctx, resourceFileInfo.Size, "INR")
	if err != nil {
		return nil, err
	}
	L.Info("aws: Estimating costs")
	L.Print(cost)
	readable, err := file_io.IsReadable(resourceFilePath)

	if err != nil || !readable {
		return nil, fmt.Errorf("could not read resource: %s", resourceFilePath)
	}

	uploadRes, err := aws.createMultipartUpload(ctx, taskKey)
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
		BlockSizeInBytes: aws.getOptimalBlockSizeForSize(int64(info.Size)),
	}, nil
}

func (aws *AwsBackend) UploadResource(
	ctx context.Context,
	taskRepo repository.TaskRepository,
	uploadRepo repository.UploadRepository,
	uploadBlockRepo repository.UploadBlockRepository,
	maxConcurrentJobs int,
	uploadId int64,
) error {
	upload, err := uploadRepo.GetUploadById(ctx, uploadId)
	if err != nil {
		return fmt.Errorf("could not find upload for upload id %d:%w", uploadId, err)
	}
	L.Printf(
		"Using up to %s to upload\n",
		L.HumanReadableCount(maxConcurrentJobs, "job", "jobs"),
	)
	task, err := taskRepo.GetTaskById(ctx, upload.TaskId)
	taskKey := task.Key()
	if err != nil {
		return fmt.Errorf("could not find task for upload id %d:%w", uploadId, err)
	}
	var awsUploadRes CreateMultipartUploadResult
	err = json.Unmarshal([]byte(upload.StorageBackendMetadataJson), &awsUploadRes)
	if err != nil {
		return fmt.Errorf("could not parse storage backend metadata for upload id %d:%w", uploadId, err)
	}
	resetCnt, err := uploadBlockRepo.ResetDirtyBlocks(ctx, uploadId)
	if err != nil {
		return err
	}
	if resetCnt > 0 {
		L.Info(fmt.Sprintf("Resetting %d dirty blocks from previous unfinished run", resetCnt))
	}
	createdCnt, err := uploadBlockRepo.CreateUploadBlocks(
		ctx,
		uploadId,
		upload.FileSize,
		upload.BlockSizeInBytes,
	)
	if err != nil {
		return err
	}
	if createdCnt > 0 {
		L.Debug(fmt.Sprintf("Upload blocks created: %d", createdCnt))
	}

	// add bytes from completed blocks
	completedBlocks, err := uploadBlockRepo.GetCompletedBlocksForUploadId(ctx, upload.Id)
	if err != nil {
		return fmt.Errorf("couldn't get existing completed blocks for upload id %d: %w", upload.Id, err)
	}
	completedBytes := int64(0)
	for _, b := range completedBlocks {
		completedBytes += b.Size
	}
	var totalSent atomic.Uint64
	totalSent.Store(uint64(completedBytes))

	// DB_BATCH_SIZE is # of next unfinished blocks to fetch from sqlite DB
	// TODO: maybe this should be exposed as arg/config?
	const DB_BATCH_SIZE = 16
	blockIds := make(chan int64, DB_BATCH_SIZE)

	// producer - get the unfinished block ids from sqlite
	go func() {
		defer close(blockIds)
		for {
			// TODO: implement error retries
			ids, err := uploadBlockRepo.ClaimNextUnfinishedBlocks(ctx, uploadId, DB_BATCH_SIZE)
			if err != nil {
				L.Panic(fmt.Sprintf("could not get next unfinished blocks for upload id %d:%v", uploadId, err))
				// TODO: handle errors
				return
			}
			L.Debug(fmt.Sprintf("Claimed blocks to run: %v", ids))
			if len(ids) == 0 {
				L.Info("Skipping UploadBlock(s) because all blocks are finished uploading.")
				break
			}

			for _, id := range ids {
				select {
				case blockIds <- id:
				case <-ctx.Done():
					return
				}
			}

			if len(ids) < DB_BATCH_SIZE {
				// no more unfinished blocks
				break
			}
		}
	}()

	sema := make(chan struct{}, maxConcurrentJobs)
	var wg sync.WaitGroup
	startTime := time.Now()

	progress := sync.Map{} // progress[workerId] = sentBytes

	// we also need maxConcurrentJobs while printing the progress line
	progress.Store("maxConcurrentJobs", maxConcurrentJobs)

	// consumer - process unfinished blocks
	// NOTE: workerIds are 1 indexed
	for workerId := 1; workerId <= maxConcurrentJobs; workerId++ {
		wg.Add(1)
		go func(workerId int) {
			defer wg.Done()
			for blockId := range blockIds {
				sema <- struct{}{}
				err = aws.uploadBlock(
					ctx,
					uploadBlockRepo,
					upload,
					&awsUploadRes,
					taskKey,
					blockId,
					workerId,
					&progress,
					&totalSent,
				)
				if err != nil {
					L.Panic(err)
				}
				<-sema
			}
		}(workerId)
	}

	waitCh := make(chan struct{}, 1)

	go func() {
		wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		delta := time.Now().UnixMilli() - startTime.UnixMilli()
		if totalSent.Load() > 0 {
			L.Footer(L.NORMAL, "")
			L.Printf("Uploading: Done (%s uploaded)\n", L.HumanReadableBytes(totalSent.Load(), 1))
			L.Printf("took %s\n", L.HumanReadableTime(delta))
		}
		return aws.completeMultipartUpload(
			ctx,
			uploadRepo,
			uploadBlockRepo,
			upload,
			&awsUploadRes,
			task)
	case <-ctx.Done():
		return ctx.Err()
	}
}
