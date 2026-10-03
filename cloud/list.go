package cloud

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Lister interface {
	List(context.Context, string, string) ([]Object, string, error)
}

func (s *s3Store) List(ctx context.Context, prefix, after string) ([]Object, string, error) {
	v, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix, StartAfter: optional(after), MaxKeys: aws.Int32(1000)})
	if err != nil {
		return nil, "", err
	}
	out := make([]Object, 0, len(v.Contents))
	last := ""
	for _, o := range v.Contents {
		key := aws.ToString(o.Key)
		out = append(out, Object{Key: key, ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.Size), ModifiedAt: o.LastModified, Class: string(o.StorageClass)})
		last = key
	}
	if !aws.ToBool(v.IsTruncated) {
		last = ""
	}
	return out, last, nil
}
func List(ctx context.Context, s Store, prefix string, fn func(Object) error) error {
	l, ok := s.(Lister)
	if !ok {
		return fmt.Errorf("cloud: remote does not support discovery")
	}
	after := ""
	for {
		objects, next, err := l.List(ctx, prefix, after)
		if err != nil {
			return err
		}
		for _, o := range objects {
			if err = fn(o); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		if next <= after {
			return fmt.Errorf("cloud: invalid listing continuation")
		}
		after = next
	}
}
