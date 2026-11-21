package cloud

import (
	"context"
	"errors"
	"fmt"
)

var ErrObjectExists = errors.New("cloud: remote key already exists")

type Creator interface {
	PutNew(context.Context, string, string, string) (Object, error)
}

func Create(ctx context.Context, store Store, key, file, class string) (Object, error) {
	h, err := store.Head(ctx, key, "")
	if err == nil {
		hash, size, err := hashFile(ctx, file)
		if err != nil {
			return Object{}, err
		}
		if h.Hash == hash && h.Size == size {
			return h, nil
		}
		return Object{}, fmt.Errorf("%w: %q", ErrObjectExists, key)
	}
	if !NotFound(err) {
		return Object{}, err
	}
	if creator, ok := store.(Creator); ok {
		return creator.PutNew(ctx, key, file, class)
	}
	return store.Put(ctx, key, file, class)
}
