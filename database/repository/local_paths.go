package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"glesha/database/model"
)

// PreserveLocalPaths keeps cache paths local without recording portable changes.
func PreserveLocalPaths(ctx context.Context, previous, incoming Cataloger) error {
	target, ok := incoming.(*catalog)
	if !ok {
		return fmt.Errorf("catalog: local paths require a catalog repository")
	}
	return incoming.EachSnapshot(ctx, func(snapshot model.Snapshot) error {
		local, err := previous.Snapshot(ctx, snapshot.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (local.Hash != snapshot.Hash || local.Size != snapshot.Size) {
			return fmt.Errorf("catalog: snapshot identity differs for %s", snapshot.ID)
		}
		snapshot.File, snapshot.ReadyFile = local.File, local.ReadyFile
		for i := range snapshot.Chunks {
			snapshot.Chunks[i].File = ""
			if i < len(local.Chunks) && snapshot.Chunks[i].Hash == local.Chunks[i].Hash {
				snapshot.Chunks[i].File = local.Chunks[i].File
			}
		}
		b, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		_, err = target.db.ExecContext(ctx, "UPDATE snapshots SET data=? WHERE id=?", string(b), snapshot.ID)
		return err
	})
}
