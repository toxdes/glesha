package cloud

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"glesha/config"
	L "glesha/logger"
)

type Object struct {
	ModifiedAt                                             *time.Time
	Key, Version, ETag, Class, Restore, Hash, Verification string
	ArchiveStatus                                          string
	ObjectLockMode, LegalHold                              string
	RetainUntil                                            *time.Time
	Size                                                   int64
	Cold                                                   bool
}
type Uploader interface {
	Put(context.Context, string, string, string) (Object, error)
}
type Reader interface {
	Get(context.Context, string, string) (io.ReadCloser, Object, error)
	Head(context.Context, string, string) (Object, error)
}
type Deleter interface {
	Delete(context.Context, string) error
}
type ClassChanger interface {
	CopyClass(context.Context, string, string, string) (Object, error)
}
type VersionRestorer interface {
	RestoreVersion(context.Context, string, string, int32, string) error
}
type Store interface {
	Uploader
	Reader
}

type s3Store struct {
	client  *s3.Client
	bucket  string
	workers int
	name    string
	limit   chan struct{}
}

func New(ctx context.Context, name string, p config.Provider, workers int, limit chan struct{}) (Store, error) {
	if workers < 1 || limit != nil && cap(limit) == 0 {
		return nil, fmt.Errorf("cloud: workers and transfer buffer capacity must be positive")
	}
	if p.Bucket == "" {
		return nil, fmt.Errorf("cloud: %s bucket is not configured", name)
	}
	var key, secret, token string
	if name == "b2" {
		key, secret = config.FirstEnv("B2_KEY_ID"), config.FirstEnv("B2_APPLICATION_KEY")
		token = ""
		if p.Endpoint == "" {
			return nil, fmt.Errorf("cloud: B2 endpoint is required")
		}
	} else if name == "aws" {
		p.Region = strings.ToLower(strings.TrimSpace(p.Region))
		key, secret = config.FirstEnv("AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY"), config.FirstEnv("AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY")
		token = config.FirstEnv("AWS_SESSION_TOKEN")
	} else {
		return nil, fmt.Errorf("cloud: unknown provider %q", name)
	}
	if key == "" || secret == "" {
		return nil, fmt.Errorf("cloud: %s credentials are missing", name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := aws.Config{Region: p.Region, HTTPClient: progressHTTPClient{client: awshttp.NewBuildableClient()}, Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(key, secret, token)), Retryer: func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 5 })
	}}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if name == "b2" {
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		}
		if p.Endpoint != "" {
			o.BaseEndpoint = aws.String(p.Endpoint)
			o.UsePathStyle = true
		}
	})
	return &s3Store{client: client, bucket: p.Bucket, workers: workers, name: name, limit: limit}, nil
}
func NotFound(e error) bool {
	var api smithy.APIError
	if errors.As(e, &api) {
		return api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound" || api.ErrorCode() == "404"
	}
	return false
}
func Conflict(e error) bool {
	var api smithy.APIError
	return errors.As(e, &api) && (api.ErrorCode() == "PreconditionFailed" || api.ErrorCode() == "ConditionalRequestConflict")
}
func norm(class types.StorageClass) string {
	if class == "" {
		return "STANDARD"
	}
	return string(class)
}
func cold(class, restore string, archived bool) bool {
	return (class == "GLACIER" || class == "DEEP_ARCHIVE" || archived) && !strings.Contains(restore, `ongoing-request="false"`)
}
func (s *s3Store) Head(ctx context.Context, key, version string) (Object, error) {
	return s.head(ctx, key, version, "")
}
func (s *s3Store) head(ctx context.Context, key, version, etag string) (Object, error) {
	in := &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key, IfMatch: optional(etag)}
	if version != "" {
		in.VersionId = &version
	}
	h, e := s.client.HeadObject(ctx, in)
	if e != nil {
		return Object{}, e
	}
	class := norm(h.StorageClass)
	restore := aws.ToString(h.Restore)
	return Object{Key: key, Version: aws.ToString(h.VersionId), ETag: aws.ToString(h.ETag), Size: aws.ToInt64(h.ContentLength), Class: class, Restore: restore, ArchiveStatus: string(h.ArchiveStatus), Cold: cold(class, restore, h.ArchiveStatus != ""), Hash: h.Metadata["sha256"], ObjectLockMode: string(h.ObjectLockMode), RetainUntil: h.ObjectLockRetainUntilDate, LegalHold: string(h.ObjectLockLegalHoldStatus), ModifiedAt: h.LastModified}, nil
}
func (s *s3Store) Get(ctx context.Context, key, version string) (io.ReadCloser, Object, error) {
	h, e := s.Head(ctx, key, version)
	if e != nil {
		return nil, h, e
	}
	if h.Cold {
		return nil, h, fmt.Errorf("cloud: %s requires explicit cold request before download", key)
	}
	version = h.Version
	in := &s3.GetObjectInput{Bucket: &s.bucket, Key: &key, IfMatch: &h.ETag}
	if version != "" {
		in.VersionId = &version
	}
	v, e := s.client.GetObject(ctx, in)
	if e != nil {
		return nil, h, e
	}
	return v.Body, h, nil
}
func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, e := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return e
}
func (s *s3Store) Put(ctx context.Context, key, file, class string) (Object, error) {
	return s.put(ctx, key, file, class, false)
}
func (s *s3Store) PutNew(ctx context.Context, key, file, class string) (Object, error) {
	return s.put(ctx, key, file, class, true)
}
func (s *s3Store) put(ctx context.Context, key, file, class string, createOnly bool) (Object, error) {
	condition := ""
	if createOnly && s.name == "aws" {
		condition = "*"
	}
	f, e := os.Open(file)
	if e != nil {
		return Object{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return Object{}, e
	}
	hash, size, e := readerHash(ctx, f)
	if e != nil {
		return Object{}, e
	}
	if size != info.Size() {
		return Object{}, fmt.Errorf("cloud: source changed during hashing")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return Object{}, e
	}
	if s.name == "b2" {
		class = ""
	}
	progress := L.StartProgress(ctx, "Uploading archive to "+s.name, info.Size())
	defer progress.Finish()
	var result Object
	if info.Size() < 5<<20 {
		if e = s.acquire(ctx); e != nil {
			return Object{}, e
		}
		var response *s3.PutObjectOutput
		digest, _ := hex.DecodeString(hash)
		uploadCtx, body := trackUpload(ctx, f, L.UploadProgress(ctx, progress))
		response, e = s.client.PutObject(uploadCtx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: body, ContentLength: aws.Int64(info.Size()), StorageClass: types.StorageClass(class), Metadata: map[string]string{"sha256": hash}, ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digest)), IfNoneMatch: optional(condition)})
		s.release()
		if e == nil {
			result = Object{Key: key, Version: aws.ToString(response.VersionId), ETag: aws.ToString(response.ETag)}
		}
	} else {
		result, e = s.multipart(ctx, key, info.Size(), class, func(ctx context.Context, id string, n int32, start, end int64) (types.CompletedPart, error) {
			uploadCtx, body := trackUpload(ctx, io.NewSectionReader(f, start, end-start), L.UploadProgress(ctx, progress))
			v, e := s.client.UploadPart(uploadCtx, &s3.UploadPartInput{Bucket: &s.bucket, Key: &key, UploadId: &id, PartNumber: &n, ContentLength: aws.Int64(end - start), Body: body})
			if e != nil {
				return types.CompletedPart{}, e
			}
			return types.CompletedPart{PartNumber: &n, ETag: v.ETag}, nil
		}, map[string]string{"sha256": hash}, nil, condition)
	}
	if e != nil {
		return Object{}, e
	}
	afterHash, afterSize, e := hashFile(L.WithProgressLabel(ctx, "Checking archive after upload"), file)
	if e != nil {
		return result, e
	}
	afterInfo, e := os.Stat(file)
	if e != nil {
		return result, e
	}
	if afterHash != hash || afterSize != info.Size() || !os.SameFile(info, afterInfo) || !info.ModTime().Equal(afterInfo.ModTime()) {
		return result, fmt.Errorf("cloud: source changed during upload")
	}
	h, e := s.verifyResult(ctx, result, info.Size())
	if e != nil {
		return result, e
	}
	if h.Hash != "" && h.Hash != hash {
		return result, fmt.Errorf("cloud: uploaded metadata hash mismatch")
	}
	return h, nil
}
func hashFile(ctx context.Context, p string) (string, int64, error) { return fileHash(ctx, p) }
func (s *s3Store) multipart(ctx context.Context, key string, size int64, class string, part func(context.Context, string, int32, int64, int64) (types.CompletedPart, error), metadata map[string]string, extra *s3.CreateMultipartUploadInput, condition string) (result Object, err error) {
	if s.workers < 1 || size < 1 {
		return result, fmt.Errorf("cloud: invalid multipart workers or object size")
	}
	in := extra
	if in == nil {
		in = &s3.CreateMultipartUploadInput{}
	}
	in.Bucket = &s.bucket
	in.Key = &key
	in.StorageClass = types.StorageClass(class)
	in.Metadata = metadata
	v, e := s.client.CreateMultipartUpload(ctx, in)
	if e != nil {
		return result, e
	}
	id := aws.ToString(v.UploadId)
	if id == "" {
		return result, fmt.Errorf("cloud: missing multipart upload ID")
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			defer cancel()
			_, _ = s.client.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: &id})
		}
	}()
	partSize := int64(5 << 20)
	if extra != nil {
		partSize = 128 << 20
	}
	if size/partSize >= 10000 {
		partSize = (size + 9998) / 9999
	}
	count := int((size + partSize - 1) / partSize)
	parts := make([]types.CompletedPart, count)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				start := int64(j) * partSize
				end := start + partSize
				if end > size {
					end = size
				}
				if e := s.acquire(ctx); e != nil {
					mu.Lock()
					if first == nil {
						first = e
					}
					mu.Unlock()
					continue
				}
				p, e := part(ctx, id, int32(j+1), start, end)
				s.release()
				if e == nil && aws.ToString(p.ETag) == "" {
					e = fmt.Errorf("cloud: missing multipart part ETag")
				}
				if e != nil {
					mu.Lock()
					if first == nil {
						first = e
						cancel()
					}
					mu.Unlock()
					continue
				}
				parts[j] = p
			}
		}()
	}
	for j := 0; j < count; j++ {
		select {
		case jobs <- j:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return result, first
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	var response *s3.CompleteMultipartUploadOutput
	response, e = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: &id, MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}, IfNoneMatch: optional(condition)})
	if e == nil {
		result = Object{Key: key, Version: aws.ToString(response.VersionId), ETag: aws.ToString(response.ETag)}
	}
	return result, e
}
func (s *s3Store) CopyClass(ctx context.Context, key, version, class string) (Object, error) {
	h, e := s.Head(ctx, key, version)
	if e != nil {
		return Object{}, e
	}
	if h.Class == class {
		return h, nil
	}
	if h.Cold {
		return Object{}, fmt.Errorf("cloud: cold source requires explicit restoration")
	}
	current, err := s.Head(ctx, key, "")
	if err != nil {
		return Object{}, err
	}
	if current.Version != h.Version || current.ETag != h.ETag {
		if current.Class == class && current.Hash != "" && current.Hash == h.Hash && current.Size == h.Size {
			return current, nil
		}
		return Object{}, fmt.Errorf("cloud: current key differs from the registered source; refusing to replace it")
	}

	if version == "" {
		version = h.Version
	}
	source := url.PathEscape(s.bucket + "/" + key)
	if version != "" {
		source += "?versionId=" + url.QueryEscape(version)
	}
	head, e := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version), IfMatch: &h.ETag})
	if e != nil {
		return Object{}, e
	}
	acl, e := s.client.GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version)})
	if e != nil {
		return Object{}, e
	}
	grants, e := grantHeaders(acl)
	if e != nil {
		return Object{}, e
	}
	var result Object
	if h.Size <= 5<<30 {
		var response *s3.CopyObjectOutput
		response, e = s.client.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &s.bucket, Key: &key, CopySource: &source, CopySourceIfMatch: &h.ETag, StorageClass: types.StorageClass(class), GrantFullControl: grants[types.PermissionFullControl], GrantRead: grants[types.PermissionRead], GrantReadACP: grants[types.PermissionReadAcp], GrantWriteACP: grants[types.PermissionWriteAcp], ObjectLockMode: head.ObjectLockMode, ObjectLockRetainUntilDate: head.ObjectLockRetainUntilDate, ObjectLockLegalHoldStatus: head.ObjectLockLegalHoldStatus, MetadataDirective: types.MetadataDirectiveCopy, TaggingDirective: types.TaggingDirectiveCopy, ServerSideEncryption: head.ServerSideEncryption, SSEKMSKeyId: head.SSEKMSKeyId, BucketKeyEnabled: head.BucketKeyEnabled})
		if e == nil {
			if response.CopyObjectResult == nil {
				return Object{}, fmt.Errorf("cloud: missing copy result")
			}
			result = Object{Key: key, Version: aws.ToString(response.VersionId), ETag: aws.ToString(response.CopyObjectResult.ETag)}
		}
	} else {
		tags, te := s.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version)})
		if te != nil {
			return Object{}, te
		}
		values := url.Values{}
		for _, t := range tags.TagSet {
			values.Add(aws.ToString(t.Key), aws.ToString(t.Value))
		}
		extra := &s3.CreateMultipartUploadInput{GrantFullControl: grants[types.PermissionFullControl], GrantRead: grants[types.PermissionRead], GrantReadACP: grants[types.PermissionReadAcp], GrantWriteACP: grants[types.PermissionWriteAcp], ObjectLockMode: head.ObjectLockMode, ObjectLockRetainUntilDate: head.ObjectLockRetainUntilDate, ObjectLockLegalHoldStatus: head.ObjectLockLegalHoldStatus, ContentType: head.ContentType, ContentEncoding: head.ContentEncoding, ContentLanguage: head.ContentLanguage, ContentDisposition: head.ContentDisposition, CacheControl: head.CacheControl, Expires: head.Expires, ServerSideEncryption: head.ServerSideEncryption, SSEKMSKeyId: head.SSEKMSKeyId, BucketKeyEnabled: head.BucketKeyEnabled, Tagging: aws.String(values.Encode())}
		result, e = s.multipart(ctx, key, h.Size, class, func(ctx context.Context, id string, n int32, start, end int64) (types.CompletedPart, error) {
			rg := "bytes=" + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end-1, 10)
			v, e := s.client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{Bucket: &s.bucket, Key: &key, UploadId: &id, PartNumber: &n, CopySource: &source, CopySourceIfMatch: &h.ETag, CopySourceRange: &rg})
			if e != nil {
				return types.CompletedPart{}, e
			}
			if v.CopyPartResult == nil {
				return types.CompletedPart{}, fmt.Errorf("cloud: missing multipart copy result")
			}
			return types.CompletedPart{PartNumber: &n, ETag: v.CopyPartResult.ETag}, nil
		}, head.Metadata, extra, "")
	}
	if e != nil {
		return Object{}, e
	}
	result, e = s.verifyResult(ctx, result, h.Size)
	if e != nil {
		return result, e
	}

	if result.Class != class {
		return result, fmt.Errorf("cloud: class verification failed")
	}
	return result, nil
}
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func (s *s3Store) RestoreVersion(ctx context.Context, key, version string, days int32, tier string) error {
	if days < 1 || tier != "Standard" && tier != "Bulk" && tier != "Expedited" {
		return fmt.Errorf("cloud: invalid cold restoration days or tier")
	}
	head, e := s.Head(ctx, key, version)
	if e != nil {
		return e
	}
	if head.Class != "GLACIER" && head.Class != "DEEP_ARCHIVE" && !(head.Class == "INTELLIGENT_TIERING" && head.ArchiveStatus != "") {
		return fmt.Errorf("cloud: object is not in a cold archive tier")
	}
	if tier == "Expedited" && (head.Class == "DEEP_ARCHIVE" || strings.Contains(head.ArchiveStatus, "DEEP")) {
		return fmt.Errorf("cloud: expedited retrieval is unavailable for Deep Archive")
	}
	restoreDays := &days
	if head.Class == "INTELLIGENT_TIERING" {
		restoreDays = nil
	}
	_, e = s.client.RestoreObject(ctx, &s3.RestoreObjectInput{Bucket: &s.bucket, Key: &key, VersionId: optional(head.Version), RestoreRequest: &types.RestoreRequest{Days: restoreDays, GlacierJobParameters: &types.GlacierJobParameters{Tier: types.Tier(tier)}}})
	return e
}

func grantHeaders(acl *s3.GetObjectAclOutput) (map[types.Permission]*string, error) {
	out := map[types.Permission]*string{}
	if len(acl.Grants) == 1 && acl.Grants[0].Permission == types.PermissionFullControl && acl.Grants[0].Grantee != nil && acl.Owner != nil && aws.ToString(acl.Grants[0].Grantee.ID) == aws.ToString(acl.Owner.ID) {
		return out, nil
	}
	values := map[types.Permission][]string{}
	for _, g := range acl.Grants {
		if g.Grantee == nil {
			return nil, fmt.Errorf("cloud: missing ACL grantee")
		}
		var value string
		switch g.Grantee.Type {
		case types.TypeCanonicalUser:
			value = "id=" + strconv.Quote(aws.ToString(g.Grantee.ID))
		case types.TypeGroup:
			value = "uri=" + strconv.Quote(aws.ToString(g.Grantee.URI))
		case types.TypeAmazonCustomerByEmail:
			value = "emailAddress=" + strconv.Quote(aws.ToString(g.Grantee.EmailAddress))
		default:
			return nil, fmt.Errorf("cloud: unsupported ACL grantee")
		}
		switch g.Permission {
		case types.PermissionFullControl, types.PermissionRead, types.PermissionReadAcp, types.PermissionWriteAcp:
		default:
			return nil, fmt.Errorf("cloud: unsupported ACL permission")
		}
		values[g.Permission] = append(values[g.Permission], value)
	}
	for permission, parts := range values {
		out[permission] = aws.String(strings.Join(parts, ","))
	}
	return out, nil
}

func (s *s3Store) acquire(ctx context.Context) error {
	if s.limit == nil {
		return ctx.Err()
	}
	select {
	case s.limit <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *s3Store) release() {
	if s.limit != nil {
		<-s.limit
	}
}

func (s *s3Store) verifyResult(ctx context.Context, result Object, size int64) (Object, error) {
	if result.ETag == "" {
		return result, fmt.Errorf("cloud: response has no object ETag")
	}
	h, e := s.head(ctx, result.Key, result.Version, result.ETag)
	if e != nil {
		return result, e
	}
	if h.ETag != result.ETag || result.Version != "" && h.Version != result.Version || h.Size != size {
		return result, fmt.Errorf("cloud: returned object identity or size does not match response")
	}
	h.Verification = "metadata"
	return h, nil
}
