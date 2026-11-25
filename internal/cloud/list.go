package cloud

import (
	"context"
	"fmt"
)

type Lister interface {
	List(context.Context, string, string) ([]Object, string, error)
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
