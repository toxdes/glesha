package app_cmd

import (
	"errors"
	"fmt"
)

var ErrRemoteExcluded = errors.New("cli: required remote is outside the --from/--to selection")

type remoteScope struct {
	names []string
	ids   map[string]bool
}

func (r *runtime) selectRemotes(names []string) error {
	if len(names) == 0 {
		r.remoteScope = nil
		return nil
	}
	ids, err := r.remoteIDs(names)
	if err != nil {
		return err
	}
	scope := &remoteScope{names: append([]string(nil), names...), ids: make(map[string]bool, len(ids))}
	for _, id := range ids {
		scope.ids[id] = true
	}
	r.remoteScope = scope
	if r.service != nil {
		for id := range r.service.Stores {
			if !scope.ids[id] {
				delete(r.service.Stores, id)
				delete(r.service.Kinds, id)
			}
		}
	}
	return nil
}

func (r *runtime) checkRemote(id string) error {
	if r.remoteScope != nil && !r.remoteScope.ids[id] {
		name := r.remoteLabel(id)
		return fmt.Errorf("%w: %s; include its configured remote name in --from or --to", ErrRemoteExcluded, name)
	}
	return nil
}

func (r *runtime) selectedRemotes() []string {
	if r.remoteScope != nil {
		return r.remoteScope.names
	}
	return r.configuredRemotes()
}

func (r *runtime) selectCommandRemotes() error {
	return r.selectRemotes(append(append([]string{}, r.env.From...), r.env.To...))
}
