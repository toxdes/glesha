package backup

import (
	"context"
	"encoding/json"

	"glesha/database/model"
)

func portableSet(set model.Set) model.Set {
	set.Roots = append([]model.Root(nil), set.Roots...)
	for i := range set.Roots {
		set.Roots[i].Path = ""
	}
	return set
}

func (s *Service) SaveConfiguration(ctx context.Context, previous model.Set) error {
	before, err := json.Marshal(portableSet(previous))
	if err != nil {
		return err
	}
	after, err := json.Marshal(portableSet(s.Set))
	if err != nil {
		return err
	}
	if string(before) == string(after) {
		return nil
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.SetMeta(ctx, "dirty", "configuration")
}
