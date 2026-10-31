package app_cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"glesha/backup"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
)

type catalogSyncResult struct {
	Set    string              `json:"set"`
	State  backup.CatalogState `json:"state"`
	Error  string              `json:"error,omitempty"`
	Reason string              `json:"reason,omitempty"`
	Result CatalogSyncOutcome  `json:"result,omitempty"`
	Locked bool                `json:"locked"`
}

const maximumCatalogReconnects = 16

type discoveryReport struct {
	Version  model.DiscoveryVersion `json:"version"`
	Complete bool                   `json:"complete"`
	At       time.Time              `json:"at"`
	Remotes  []string               `json:"remotes"`
	Failures []string               `json:"failures,omitempty"`
}

func (r *runtime) configuredRemotes() []string {
	var names []string
	for name, remote := range r.config.RemotesByName() {
		if remote.Bucket != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *runtime) cacheCatalog() error {
	result, err := r.cacheCatalogWithResult()
	r.catalogCacheUpdated = r.catalogCacheUpdated || result.Updated
	return err
}

func (r *runtime) cacheCatalogWithResult() (backup.CatalogCacheResult, error) {
	if !r.catalogCheckActive {
		progress, err := r.startActivity("Checking catalog metadata")
		if err != nil {
			return backup.CatalogCacheResult{}, err
		}
		defer progress.Stop()
	}
	for attempts := 0; attempts < maximumCatalogReconnects; attempts++ {
		if err := r.syncStores(); err != nil {
			return backup.CatalogCacheResult{}, err
		}
		result, err := r.service.CacheCatalogWithResult(L.WithProgress(r.ctx, nil))
		if !errors.Is(err, backup.ErrAuthorityMoved) {
			return result, err
		}
	}
	return backup.CatalogCacheResult{}, fmt.Errorf("cli: too many catalog authority changes; repeat catalog sync")
}

func (r *runtime) discoveryStatus() error {
	b, err := os.ReadFile(filepath.Join(r.root, "discovery-status.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var report discoveryReport
	if err = json.Unmarshal(b, &report); err != nil {
		return err
	}
	if err = report.Version.Validate(); err != nil {
		return err
	}
	if !report.Complete {
		return fmt.Errorf("cli: last bucket discovery was incomplete; repeat glesha catalog sync")
	}
	return nil
}

func (r *runtime) discoverState(names []string) error {
	if err := r.selectCommandRemotes(); err != nil {
		return err
	}
	if len(names) == 0 {
		names = r.selectedRemotes()
	}
	rawService(r)
	var failures []error
	report := discoveryReport{Version: model.DiscoveryVersionInitial, Complete: true, At: time.Now().UTC(), Remotes: names}
	for _, name := range names {
		ids, err := r.remoteIDs([]string{name})
		if err == nil {
			err = r.stores(ids)
		}
		if err == nil {
			err = r.service.DiscoverState(r.ctx, ids[0])
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("cli: discover %s: %w", name, err))
			report.Complete = false
			report.Failures = append(report.Failures, name+": "+err.Error())
		}
	}
	b, err := json.Marshal(report)
	if err == nil {
		err = file_io.WriteAtomic(context.WithoutCancel(r.ctx), filepath.Join(r.root, "discovery-status.json"), b)
	}
	return errors.Join(errors.Join(failures...), err)
}

func (r *runtime) unlockCatalog() error {
	if err := r.service.CachedEncryption(r.ctx); errors.Is(err, os.ErrNotExist) {
		if err = r.syncStores(); err != nil {
			return err
		}
		if err = r.cacheCatalog(); err != nil {
			return err
		}
		if err = r.service.CachedEncryption(r.ctx); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := r.catalogSecret(false); err != nil {
		return err
	}
	if err := r.service.UnlockCached(r.ctx); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = r.syncStores(); err != nil {
			return err
		}
		if err = r.cacheCatalog(); err != nil {
			return err
		}
		return r.service.UnlockCached(r.ctx)
	}
	return nil
}

func (r *runtime) readableCatalog() error {
	if _, err := os.Stat(filepath.Join(r.service.Directory, "catalog.db")); err == nil {
		ahead, err := r.service.DownloadedCatalogAhead(r.ctx)
		if err != nil || !ahead {
			return err
		}
		return r.unlockCatalog()
	} else if !os.IsNotExist(err) {
		return err
	}
	if r.service.Set.CatalogTo == "" || r.service.Set.CatalogTo == "local" {
		return nil
	}
	return r.unlockCatalog()
}

func (r *runtime) prepareMutation() error {
	s := r.service
	if s.Pending() || s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return nil
	}
	if err := r.syncStores(); err != nil {
		return err
	}
	err := r.cacheCatalog()
	if errors.Is(err, backup.ErrNoRemoteCatalog) && s.Set.Name != s.Set.ID {
		c, openErr := s.Catalog(r.ctx)
		if openErr != nil {
			return openErr
		}
		if closeErr := c.Close(); closeErr != nil {
			return closeErr
		}
	} else if err != nil {
		return err
	} else if err = s.CachedEncryption(r.ctx); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.Directory, "catalog.db")); errors.Is(err, os.ErrNotExist) {
		if err = r.readableCatalog(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := r.catalogSecret(true); err != nil {
		return err
	}
	if err := s.SyncCatalog(r.ctx); err != nil {
		return err
	}
	return s.RefreshSet(r.ctx)
}

func (r *runtime) publishState() error {
	s := r.service
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return nil
	}
	if err := r.syncStores(); err != nil {
		return err
	}
	if err := r.catalogSecret(true); err != nil {
		return err
	}
	if err := s.SyncCatalog(r.ctx); err != nil {
		return fmt.Errorf("cli: state publication incomplete; run catalog sync %s: %w", s.Set.ID, err)
	}
	return r.replicateDirectory()
}

func (r *runtime) replicateDirectory() error {
	if err := r.selectCommandRemotes(); err != nil {
		return err
	}
	if _, err := r.service.DirectoryRepairPending(r.ctx); err != nil {
		return err
	}
	var failures []error
	for _, name := range r.selectedRemotes() {
		ids, err := r.remoteIDs([]string{name})
		if err == nil {
			err = r.stores(ids)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("cli: registry replica %s: %w", name, err))
		}
	}
	updated, err := r.service.PublishDirectoryWithResult(r.ctx)
	r.catalogReplicasUpdated += updated
	if err != nil {
		failures = append(failures, err)
	}
	failure := errors.Join(failures...)
	return errors.Join(failure, r.service.RecordDirectoryRepair(r.ctx, failure))
}

func catalogDiscover(r *runtime) error {
	r.catalogReplicasUpdated = 0
	summary := catalogSyncSummary{}
	failures := catalogFailures{}
	discoveryErr := r.discoverState(r.env.From)
	if discoveryErr != nil {
		summary.Failed++
		if err := failures.addOperation(r, "Discovery", discoveryErr); err != nil {
			return errors.Join(discoveryErr, err)
		}
	}
	stores, kinds := r.service.Stores, r.service.Kinds
	var sets []model.Set
	if err := r.registry.Each(r.ctx, func(set model.Set) error { sets = append(sets, set); return nil }); err != nil {
		return errors.Join(discoveryErr, err)
	}
	for _, set := range sets {
		if set.CatalogTo == "" || set.CatalogTo == "local" {
			summary.Skipped++
			summary.localOnly++
			if !r.env.jsonOutput() {
				continue
			}
			if err := r.emit("catalog_state", catalogSyncResult{Set: set.ID, State: backup.CatalogStateSkipped}, fmt.Sprintf("%s  SKIPPED  Local-only set", L.ASCII(set.Name))); err != nil {
				return err
			}
			continue
		}
		err := func() error {
			if err := r.openSet(set.ID); err != nil {
				return err
			}
			if err := r.checkRemote(r.service.Set.CatalogTo); err != nil {
				summary.Skipped++
				summary.outOfScope++
				if !r.env.jsonOutput() {
					return nil
				}
				return r.emit("catalog_state", catalogSyncResult{Set: set.ID, State: backup.CatalogStateSkipped, Reason: err.Error()}, fmt.Sprintf("%s  SKIPPED  %s", set.ID, L.ASCII(err.Error())))
			}
			r.service.Stores, r.service.Kinds = stores, kinds
			release, err := r.lock()
			if err != nil {
				return err
			}
			defer release()
			if err = r.syncStores(); err != nil {
				return err
			}
			before, err := r.localCatalogRevision()
			if err != nil {
				return err
			}
			cached, err := r.cacheCatalogWithResult()
			if err != nil {
				return err
			}
			state := backup.CatalogStateDownloaded
			if set.Name == set.ID {
				state = backup.CatalogStateLocked
			}
			if err = r.service.CachedEncryption(r.ctx); err != nil {
				return err
			}
			locked, err := r.service.CatalogLocked(r.ctx)
			if err != nil {
				return err
			}
			ahead := false
			if !locked {
				ahead, err = r.service.DownloadedCatalogAhead(r.ctx)
				if err != nil {
					return err
				}
			}
			if !r.service.Config.Catalog.Encrypted {
				if locked || ahead {
					if err = r.service.UnlockCached(r.ctx); err != nil {
						return err
					}
				}
				if err = r.service.SyncCatalog(r.ctx); err != nil {
					return err
				}
				locked = false
				state = backup.CatalogStateSynced
			} else {
				locked = locked || ahead
			}
			after, err := r.localCatalogRevision()
			if err != nil {
				return err
			}
			return r.emitCatalogResult(state, cached.Updated || before != after, locked, &summary)
		}()
		if err != nil {
			summary.Failed++
			if outputErr := failures.addSet(r, set, err); outputErr != nil {
				return errors.Join(err, outputErr)
			}
		}
	}
	if r.service == nil {
		r.service = &backup.Service{Config: r.config, Registry: r.registry, Directory: r.root, Stores: map[string]cloud.Store{}, Kinds: map[string]string{}}
	}
	if err := r.replicateDirectory(); err != nil {
		summary.Failed++
		if outputErr := failures.addOperation(r, "Registry replication", err); outputErr != nil {
			return errors.Join(err, outputErr)
		}
	}
	return failures.finish(r, summary)
}
