package repository

import (
	"context"
	"glesha/database"
	"glesha/database/model"
	L "glesha/logger"
)

type FileCatalogRepository interface {
	AddMany(ctx context.Context, entries []model.FileCatalogRow) error
	GetByParentPath(ctx context.Context, taskId int64, parentPath string) ([]model.FileCatalogRow, error)
}

type fileCatalogRepository struct {
	db *database.DB
}

func NewFileCatalogRepository(db *database.DB) FileCatalogRepository {
	return &fileCatalogRepository{db: db}
}

func (r *fileCatalogRepository) AddMany(ctx context.Context, entries []model.FileCatalogRow) error {
	tx, err := r.db.D.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := `
  INSERT INTO file_catalog
  (task_id,
  full_path,
  name,
  parent_path,
  file_type,
  size_bytes,
  modified_at)
  VALUES
  (?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		_, err = stmt.ExecContext(ctx, e.TaskId, e.FullPath, e.Name, e.ParentPath, e.FileType, e.SizeBytes, database.ToTimeStr(e.ModifiedAt))
		if err != nil {
			err1 := tx.Rollback()
			if err1 != nil {
				return err1
			}
			L.Debug("db: AddMany failure rollback success.")
			return err
		}
	}
	return tx.Commit()
}

func (r *fileCatalogRepository) GetByParentPath(ctx context.Context, taskId int64, parentPath string) ([]model.FileCatalogRow, error) {

	q := `
  SELECT
  id,
  task_id,
  full_path,
  name,
  parent_path,
  file_type,
  size_bytes,
  modified_at
  FROM file_catalog
  WHERE task_id = ? AND parent_path = ?
  ORDER BY file_type DESC, name ASC
  `
	rows, err := r.db.D.QueryContext(ctx,
		q,
		taskId,
		parentPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []model.FileCatalogRow
	for rows.Next() {
		var e model.FileCatalogRow
		var modAtStr string
		if err := rows.Scan(&e.Id, &e.TaskId, &e.FullPath, &e.Name, &e.ParentPath, &e.FileType, &e.SizeBytes, &modAtStr); err != nil {
			return nil, err
		}
		e.ModifiedAt = database.FromTimeStr(modAtStr)
		entries = append(entries, e)
	}
	return entries, nil
}
