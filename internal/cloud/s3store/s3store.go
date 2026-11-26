// Package s3store implements cloud.Store for S3-compatible providers.
package s3store

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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"glesha/internal/cloud"
	"glesha/internal/logger"
)

type s3Store struct {
	client   *s3.Client
	bucket   string
	workers  int
	provider cloud.Provider
	limit    chan struct{}
}

// New opens a store for the remote p of an S3-compatible provider.
func New(ctx context.Context, p cloud.Remote, workers int, limit chan struct{}) (cloud.Store, error) {
	if workers < 1 || limit != nil && cap(limit) == 0 {
		return nil, fmt.Errorf("cloud: workers and transfer buffer capacity must be positive")
	}
	if p.Bucket == "" {
		return nil, fmt.Errorf("cloud: %s bucket is not configured", p.Kind)
	}
	provider, err := cloud.Lookup(p.Kind)
	if err != nil {
		return nil, err
	}
	if provider.S3 == nil {
		return nil, fmt.Errorf("cloud: %s is not S3-compatible", p.Kind)
	}
	if provider.S3.RequireEndpoint && p.Endpoint == "" {
		return nil, fmt.Errorf("cloud: %s endpoint is required", strings.ToUpper(p.Kind))
	}
	if provider.S3.NormalizeRegion {
		p.Region = strings.ToLower(strings.TrimSpace(p.Region))
	}
	if err := provider.ValidateCredentials(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, secret, token := provider.Credentials()
	cfg := aws.Config{Region: p.Region, HTTPClient: progressHTTPClient{client: awshttp.NewBuildableClient()}, Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(key, secret, token)), Retryer: func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = 5 })
	}}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if provider.S3.RelaxedChecksums {
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		}
		if p.Endpoint != "" {
			o.BaseEndpoint = aws.String(p.Endpoint)
			o.UsePathStyle = true
		}
	})
	return &s3Store{client: client, bucket: p.Bucket, workers: workers, provider: provider, limit: limit}, nil
}

func (s *s3Store) Provider() cloud.Provider { return s.provider }
func norm(class types.StorageClass) string {
	if class == "" {
		return "STANDARD"
	}
	return string(class)
}

func cold(class, restore string, archived bool) bool {
	return (class == "GLACIER" || class == "DEEP_ARCHIVE" || archived) && !strings.Contains(restore, `ongoing-request="false"`)
}
func (s *s3Store) Head(ctx context.Context, key, version string) (o cloud.Object, err error) {
	defer classify(&err)
	return s.head(ctx, key, version, "")
}
func (s *s3Store) head(ctx context.Context, key, version, etag string) (cloud.Object, error) {
	in := &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key, IfMatch: optional(etag)}
	if version != "" {
		in.VersionId = &version
	}
	h, e := s.client.HeadObject(ctx, in)
	if e != nil {
		return cloud.Object{}, e
	}
	class := norm(h.StorageClass)
	restore := aws.ToString(h.Restore)
	return cloud.Object{Key: key, Version: aws.ToString(h.VersionId), ETag: aws.ToString(h.ETag), Size: aws.ToInt64(h.ContentLength), Class: class, Restore: restore, ArchiveStatus: string(h.ArchiveStatus), Cold: cold(class, restore, h.ArchiveStatus != ""), Hash: h.Metadata["sha256"], ObjectLockMode: string(h.ObjectLockMode), RetainUntil: h.ObjectLockRetainUntilDate, LegalHold: string(h.ObjectLockLegalHoldStatus), ModifiedAt: h.LastModified}, nil
}
func (s *s3Store) Get(ctx context.Context, key, version string) (body io.ReadCloser, o cloud.Object, err error) {
	defer classify(&err)
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
func (s *s3Store) Delete(ctx context.Context, key string) (err error) {
	defer classify(&err)
	_, e := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return e
}
func (s *s3Store) Put(ctx context.Context, key, file, class string) (o cloud.Object, err error) {
	defer classify(&err)
	return s.put(ctx, key, file, class, false)
}
func (s *s3Store) PutNew(ctx context.Context, key, file, class string) (o cloud.Object, err error) {
	defer classify(&err)
	return s.put(ctx, key, file, class, true)
}
func (s *s3Store) put(ctx context.Context, key, file, class string, createOnly bool) (cloud.Object, error) {
	condition := ""
	if createOnly && s.provider.Capabilities.ConditionalWrites {
		condition = "*"
	}
	f, e := os.Open(file)
	if e != nil {
		return cloud.Object{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return cloud.Object{}, e
	}
	hash, size, e := cloud.HashReader(ctx, f)
	if e != nil {
		return cloud.Object{}, e
	}
	if size != info.Size() {
		return cloud.Object{}, fmt.Errorf("cloud: source changed during hashing")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return cloud.Object{}, e
	}
	if !s.provider.Capabilities.StorageClasses {
		class = ""
	}
	workers := 1
	if info.Size() >= 5<<20 {
		_, parts := multipartPlan(info.Size(), false)
		workers = s.multipartWorkers(parts)
	}
	progress := logger.StartWorkerProgress(ctx, "Uploading "+cloud.TransferLabel(ctx)+" to "+string(s.provider.Kind), info.Size(), workers)
	defer progress.Finish()
	var result cloud.Object
	if info.Size() < 5<<20 {
		if e = s.acquire(ctx); e != nil {
			return cloud.Object{}, e
		}
		var response *s3.PutObjectOutput
		digest, _ := hex.DecodeString(hash)
		uploadCtx, body := trackUpload(ctx, f, logger.UploadProgress(ctx, progress))
		response, e = s.client.PutObject(uploadCtx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: body, ContentLength: aws.Int64(info.Size()), StorageClass: types.StorageClass(class), Metadata: map[string]string{"sha256": hash}, ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digest)), IfNoneMatch: optional(condition)})
		s.release()
		if e == nil {
			result = cloud.Object{Key: key, Version: aws.ToString(response.VersionId), ETag: aws.ToString(response.ETag)}
		}
	} else {
		result, e = s.multipart(ctx, key, info.Size(), class, func(ctx context.Context, id string, n int32, start, end int64) (types.CompletedPart, error) {
			uploadCtx, body := trackUpload(ctx, io.NewSectionReader(f, start, end-start), logger.UploadProgress(ctx, progress))
			v, e := s.client.UploadPart(uploadCtx, &s3.UploadPartInput{Bucket: &s.bucket, Key: &key, UploadId: &id, PartNumber: &n, ContentLength: aws.Int64(end - start), Body: body})
			if e != nil {
				return types.CompletedPart{}, e
			}
			return types.CompletedPart{PartNumber: &n, ETag: v.ETag}, nil
		}, map[string]string{"sha256": hash}, nil, condition)
	}
	if e != nil {
		return cloud.Object{}, e
	}
	afterHash, afterSize, e := cloud.HashFile(logger.WithProgressLabel(ctx, "Checking "+cloud.TransferLabel(ctx)+" after upload"), file)
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
func (s *s3Store) multipart(ctx context.Context, key string, size int64, class string, part func(context.Context, string, int32, int64, int64) (types.CompletedPart, error), metadata map[string]string, extra *s3.CreateMultipartUploadInput, condition string) (result cloud.Object, err error) {
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
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cloud.CleanupTimeout)
			defer cancel()
			_, _ = s.client.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: &id})
		}
	}()
	partSize, count := multipartPlan(size, extra != nil)
	parts := make([]types.CompletedPart, count)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for i := 0; i < s.multipartWorkers(count); i++ {
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
		result = cloud.Object{Key: key, Version: aws.ToString(response.VersionId), ETag: aws.ToString(response.ETag)}
	}
	return result, e
}
func multipartPlan(size int64, copying bool) (int64, int) {
	partSize := int64(5 << 20)
	if copying {
		partSize = 128 << 20
	}
	if size/partSize >= 10000 {
		partSize = (size + 9998) / 9999
	}
	return partSize, int((size + partSize - 1) / partSize)
}
func (s *s3Store) multipartWorkers(parts int) int {
	workers := min(s.workers, parts)
	if s.limit != nil {
		workers = min(workers, cap(s.limit))
	}
	return workers
}
func (s *s3Store) CopyClass(ctx context.Context, key, version, class string) (o cloud.Object, err error) {
	defer classify(&err)
	h, e := s.Head(ctx, key, version)
	if e != nil {
		return cloud.Object{}, e
	}
	if h.Class == class {
		return h, nil
	}
	if h.Cold {
		return cloud.Object{}, fmt.Errorf("cloud: cold source requires explicit restoration")
	}
	current, err := s.Head(ctx, key, "")
	if err != nil {
		return cloud.Object{}, err
	}
	if current.Version != h.Version || current.ETag != h.ETag {
		if current.Class == class && current.Hash != "" && current.Hash == h.Hash && current.Size == h.Size {
			return current, nil
		}
		return cloud.Object{}, fmt.Errorf("cloud: current key differs from the registered source; refusing to replace it")
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
		return cloud.Object{}, e
	}
	acl, e := s.client.GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version)})
	if e != nil {
		return cloud.Object{}, e
	}
	grants, e := grantHeaders(acl)
	if e != nil {
		return cloud.Object{}, e
	}
	var result cloud.Object
	if h.Size <= 5<<30 {
		var response *s3.CopyObjectOutput
		response, e = s.client.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &s.bucket, Key: &key, CopySource: &source, CopySourceIfMatch: &h.ETag, StorageClass: types.StorageClass(class), GrantFullControl: grants[types.PermissionFullControl], GrantRead: grants[types.PermissionRead], GrantReadACP: grants[types.PermissionReadAcp], GrantWriteACP: grants[types.PermissionWriteAcp], ObjectLockMode: head.ObjectLockMode, ObjectLockRetainUntilDate: head.ObjectLockRetainUntilDate, ObjectLockLegalHoldStatus: head.ObjectLockLegalHoldStatus, MetadataDirective: types.MetadataDirectiveCopy, TaggingDirective: types.TaggingDirectiveCopy, ServerSideEncryption: head.ServerSideEncryption, SSEKMSKeyId: head.SSEKMSKeyId, BucketKeyEnabled: head.BucketKeyEnabled})
		if e == nil {
			if response.CopyObjectResult == nil {
				return cloud.Object{}, fmt.Errorf("cloud: missing copy result")
			}
			result = cloud.Object{Key: key, Version: aws.ToString(response.VersionId), ETag: aws.ToString(response.CopyObjectResult.ETag)}
		}
	} else {
		tags, te := s.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version)})
		if te != nil {
			return cloud.Object{}, te
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
		return cloud.Object{}, e
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
func (s *s3Store) RestoreVersion(ctx context.Context, key, version string, days int32, tier string) (err error) {
	defer classify(&err)
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
	if tier == "Expedited" && head.DeepArchive() {
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

func (s *s3Store) verifyResult(ctx context.Context, result cloud.Object, size int64) (cloud.Object, error) {
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

func (s *s3Store) CheckTemporaryWrites(ctx context.Context) error {
	configuration, err := s.client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: &s.bucket})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "ObjectLockConfigurationNotFoundError" || api.ErrorCode() == "NoSuchObjectLockConfiguration") {
			return nil
		}
		return fmt.Errorf("cloud: cannot verify temporary objects can be deleted before check: %w", err)
	}
	c := configuration.ObjectLockConfiguration
	if c != nil && c.Rule != nil && c.Rule.DefaultRetention != nil {
		r := c.Rule.DefaultRetention
		if r.Days != nil && *r.Days > 0 || r.Years != nil && *r.Years > 0 {
			return fmt.Errorf("cloud: check refused temporary upload: bucket has default %s Object Lock retention", r.Mode)
		}
	}
	return nil
}

func (s *s3Store) DeleteVersion(ctx context.Context, key, version string) (err error) {
	defer classify(&err)
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version)})
	return err
}

func (s *s3Store) List(ctx context.Context, prefix, after string) (objects []cloud.Object, next string, err error) {
	defer classify(&err)
	v, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix, StartAfter: optional(after), MaxKeys: aws.Int32(1000)})
	if err != nil {
		return nil, "", err
	}
	out := make([]cloud.Object, 0, len(v.Contents))
	last := ""
	for _, o := range v.Contents {
		key := aws.ToString(o.Key)
		out = append(out, cloud.Object{Key: key, ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.Size), ModifiedAt: o.LastModified, Class: string(o.StorageClass)})
		last = key
	}
	if !aws.ToBool(v.IsTruncated) {
		last = ""
	}
	return out, last, nil
}

// classify tags S3 error codes with the matching cloud sentinel error.
func classify(err *error) {
	var api smithy.APIError
	if *err == nil || !errors.As(*err, &api) {
		return
	}
	switch api.ErrorCode() {
	case "NoSuchKey", "NotFound", "404":
		*err = tagged{*err, cloud.ErrNotFound}
	case "PreconditionFailed", "ConditionalRequestConflict":
		*err = tagged{*err, cloud.ErrConflict}
	}
}

// tagged keeps the provider's message while matching kind with errors.Is.
type tagged struct{ err, kind error }

func (t tagged) Error() string   { return t.err.Error() }
func (t tagged) Unwrap() []error { return []error{t.err, t.kind} }
