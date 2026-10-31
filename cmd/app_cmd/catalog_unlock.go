package app_cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	L "glesha/logger"
)

func catalogUnlock(r *runtime, args []string) error {
	if r.env.All || r.env.Keep != "" || r.env.Authority != "" {
		return fmt.Errorf("cli: catalog unlock requires one set; use catalog sync to fetch remote state")
	}
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { wipe(r.service.CatalogPassword) }()
	if _, err := os.Stat(filepath.Join(r.service.Directory, "catalog.db")); err == nil {
		ahead, err := r.service.DownloadedCatalogAhead(r.ctx)
		if err != nil {
			return err
		}
		if !ahead {
			return r.catalogOpened(true)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := r.service.CachedEncryption(r.ctx); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cli: no cached catalog for %s; run glesha catalog sync first", r.service.Set.ID)
	} else if err != nil {
		return err
	}
	if r.service.Config.Catalog.Encrypted {
		if err := r.catalogSecret(false); err != nil {
			return err
		}
	}
	progress, err := r.startActivity("Opening cached catalog")
	if err != nil {
		return err
	}
	defer progress.Stop()
	if err := r.service.UnlockCached(L.WithProgress(r.ctx, nil)); err != nil {
		return err
	}
	progress.Finish()
	return r.catalogOpened(false)
}

func (r *runtime) catalogOpened(alreadyOpen bool) error {
	text := fmt.Sprintf("Catalog open for %q.\n  Next: glesha history %s", r.service.Set.Name, r.service.Set.ID)
	if alreadyOpen {
		text = fmt.Sprintf("Catalog already open for %q.", r.service.Set.Name)
	}
	return r.emit("catalog_unlocked", struct {
		Set         string `json:"set"`
		Name        string `json:"name"`
		AlreadyOpen bool   `json:"already_open"`
	}{r.service.Set.ID, r.service.Set.Name, alreadyOpen}, text)
}
