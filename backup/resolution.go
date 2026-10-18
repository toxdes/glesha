package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"glesha/database/model"
	"glesha/database/repository"
)

type ResolutionDifference struct {
	Field  string
	Local  string
	Remote string
}

func (s *Service) ResolutionDifferences(ctx context.Context, show func(ResolutionDifference) error) error {
	file := s.path()
	if s.Pending() {
		file = s.pending()
	}
	local, err := repository.NewCatalogRepository(ctx, file)
	if err != nil {
		return err
	}
	defer local.Close()
	if s.Pending() {
		op, err := local.GetMeta(ctx, "operation")
		if err != nil {
			return err
		}
		if op != "push" {
			return fmt.Errorf("%w: finish %s before resolution", ErrPending, op)
		}
	}
	directory, err := os.MkdirTemp(s.Directory, ".glesha-resolution-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	peer := *s
	peer.Directory = directory
	if err = peer.Pull(ctx); err != nil {
		return err
	}
	remote, err := peer.Catalog(ctx)
	if err != nil {
		return err
	}
	defer remote.Close()
	localHead, err := local.GetMeta(ctx, "head")
	if err != nil {
		return err
	}
	remoteHead, err := remote.GetMeta(ctx, "head")
	if err != nil {
		return err
	}
	if localHead != remoteHead {
		if err = show(ResolutionDifference{Field: "Incremental starting snapshot", Local: localHead, Remote: remoteHead}); err != nil {
			return err
		}
	}
	return local.EachSnapshot(ctx, func(v model.Snapshot) error {
		other, err := remote.Snapshot(ctx, v.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if other.Hash != v.Hash || !sameManifest(other.Manifest, v.Manifest) {
			return fmt.Errorf("%w: archive identity differs for snapshot %s; neither copy will be replaced", ErrConflict, v.ID)
		}
		a, err := local.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		b, err := remote.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		if err = showReferenceDifferences(v.ID, a, b, show); err != nil {
			return err
		}
		if len(v.Chunks) != len(other.Chunks) {
			return fmt.Errorf("%w: chunk identities differ", ErrConflict)
		}
		for i, ch := range v.Chunks {
			if err = showReferenceDifferences(fmt.Sprintf("%s chunk %d", v.ID, i+1), ch.Locations, other.Chunks[i].Locations, show); err != nil {
				return err
			}
		}
		return nil
	})
}

func showReferenceDifferences(snapshot string, a, b []model.Location, show func(ResolutionDifference) error) error {
	byProvider := map[string]model.Location{}
	for _, l := range b {
		byProvider[l.Provider] = l
	}
	reference := func(l model.Location) string {
		if l.Key == "" {
			return "-"
		}
		return fmt.Sprintf("%s (version %s)", l.Key, l.Version)
	}
	for _, l := range a {
		other := byProvider[l.Provider]
		if !sameReference(l, other) {
			if err := show(ResolutionDifference{Field: "Snapshot " + snapshot + " on " + l.Provider, Local: reference(l), Remote: reference(other)}); err != nil {
				return err
			}
		}
		delete(byProvider, l.Provider)
	}
	for _, l := range b {
		if _, ok := byProvider[l.Provider]; ok {
			if err := show(ResolutionDifference{Field: "Snapshot " + snapshot + " on " + l.Provider, Local: "-", Remote: reference(l)}); err != nil {
				return err
			}
		}
	}
	return nil
}
