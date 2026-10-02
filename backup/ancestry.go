package backup

import (
	"context"
	"errors"
	"fmt"
	"os"

	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
)

type snapshotChain struct {
	repository.SnapshotSequencer
	file string
}

func (s *snapshotChain) Close() error {
	return errors.Join(s.SnapshotSequencer.Close(), os.Remove(s.file))
}

func (s *Service) ancestry(ctx context.Context, c repository.Cataloger, target model.Snapshot) (repository.SnapshotSequencer, error) {
	f, err := file_io.Temp(s.Directory)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	sequence, err := repository.NewSnapshotSequence(ctx, f.Name())
	if err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	chain := &snapshotChain{SnapshotSequencer: sequence, file: f.Name()}
	v := target
	for {
		if v.Status != model.STATUS_COMPLETED {
			chain.Close()
			return nil, fmt.Errorf("backup: ancestry contains an uncommitted snapshot")
		}
		if err = chain.Append(ctx, v); err != nil {
			chain.Close()
			return nil, err
		}
		if v.Full {
			return chain, nil
		}
		if v.Parent == "" {
			chain.Close()
			return nil, fmt.Errorf("backup: chain has no full baseline")
		}
		v, err = c.Snapshot(ctx, v.Parent)
		if err != nil {
			chain.Close()
			return nil, err
		}
	}
}
