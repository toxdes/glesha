package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type VersionDeleter interface {
	DeleteVersion(context.Context, string, string) error
}

type TemporaryWriteChecker interface {
	CheckTemporaryWrites(context.Context) error
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

func (s *s3Store) DeleteVersion(ctx context.Context, key, version string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key, VersionId: optional(version)})
	return err
}

type CheckStatus string

const (
	CHECK_PASSED  CheckStatus = "OK"
	CHECK_FAILED  CheckStatus = "FAILED"
	CHECK_SKIPPED CheckStatus = "SKIPPED"
)

func (s CheckStatus) String() string { return string(s) }

type CheckStep struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}
type CheckReport struct {
	Steps   []CheckStep `json:"steps"`
	Key     string      `json:"temporary_key,omitempty"`
	Version string      `json:"version_id,omitempty"`
	Bytes   int64       `json:"bytes"`
}

func NewCheckReport() CheckReport {
	v := CheckReport{}
	for _, name := range []string{"Safety", "Write", "Metadata", "Read", "Cleanup"} {
		v.Steps = append(v.Steps, CheckStep{Name: name, Status: CHECK_SKIPPED, Detail: "not tested"})
	}
	return v
}
func (v *CheckReport) step(index int, err error, detail string) {
	v.Steps[index].Status = CHECK_PASSED
	v.Steps[index].Detail = detail
	if err != nil {
		v.Steps[index].Status = CHECK_FAILED
		v.Steps[index].Detail = err.Error()
	}
}

func Check(ctx context.Context, store Store, key, file string) error {
	_, err := CheckAccess(ctx, store, key, file)
	return err
}

func CheckAccess(ctx context.Context, store Store, key, file string) (report CheckReport, err error) {
	report = NewCheckReport()
	expected, size, err := hashFile(ctx, file)
	if err != nil {
		return report, err
	}
	report.Bytes = size
	if checker, ok := store.(TemporaryWriteChecker); ok {
		err = checker.CheckTemporaryWrites(ctx)
		report.step(0, err, "temporary objects are not subject to default retention")
		if err != nil {
			return report, err
		}
	} else {
		report.Steps[0].Detail = "retention metadata is unavailable"
	}
	if _, headErr := store.Head(ctx, key, ""); headErr == nil {
		err = fmt.Errorf("cloud: test key already exists; refusing to replace it")
		report.step(1, err, "")
		return report, err
	} else if !NotFound(headErr) {
		report.step(2, headErr, "")
		return report, headErr
	}
	var h Object
	if creator, ok := store.(Creator); ok {
		h, err = creator.PutNew(ctx, key, file, "STANDARD")
	} else {
		h, err = store.Put(ctx, key, file, "STANDARD")
	}
	report.step(1, err, "temporary object uploaded")
	if h.Key != "" {
		report.Key, report.Version = key, h.Version
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			var cleanupErr error
			if h.Version == "" {
				current, headErr := store.Head(cleanup, key, "")
				if headErr != nil {
					cleanupErr = headErr
				} else if current.ETag != h.ETag || current.Size != size {
					cleanupErr = fmt.Errorf("cloud: temporary object changed; refusing to delete it")
				}
			}
			if cleanupErr == nil {
				if deleter, ok := store.(VersionDeleter); ok {
					cleanupErr = deleter.DeleteVersion(cleanup, key, h.Version)
				} else if deleter, ok := store.(Deleter); ok {
					cleanupErr = deleter.Delete(cleanup, key)
				} else {
					cleanupErr = fmt.Errorf("cloud: remote cannot delete owned temporary objects")
				}
			}
			report.step(4, cleanupErr, "owned temporary object removed")
			if cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cloud: test object remains at %s (version %s): %w", key, h.Version, cleanupErr))
			}
		}()
	}
	if err != nil {
		return report, err
	}
	metadata, headErr := store.Head(ctx, key, h.Version)
	if headErr == nil && (metadata.Size != size || metadata.ETag != h.ETag || metadata.Version != h.Version) {
		headErr = fmt.Errorf("cloud: test object metadata differs")
	}
	report.step(2, headErr, "uploaded object metadata verified")
	err = errors.Join(err, headErr)
	r, downloaded, readErr := store.Get(ctx, key, h.Version)
	if readErr == nil {
		hash := sha256.New()
		var n int64
		n, readErr = io.Copy(hash, io.LimitReader(r, size+1))
		closeErr := r.Close()
		readErr = errors.Join(readErr, closeErr)
		if readErr == nil && (downloaded.ETag != h.ETag || downloaded.Version != h.Version || n != size || hex.EncodeToString(hash.Sum(nil)) != expected) {
			readErr = fmt.Errorf("cloud: check round-trip verification failed")
		}
	}
	report.step(3, readErr, "downloaded bytes match the uploaded bytes")
	return report, errors.Join(err, readErr)
}
