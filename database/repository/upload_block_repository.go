package repository

import (
	"context"
	"database/sql"
	"fmt"
	"glesha/database"
	"glesha/database/model"
	L "glesha/logger"
	"strings"
	"time"
)

type UploadBlockRepository interface {
	CreateUploadBlocks(
		ctx context.Context,
		uploadId int64,
		fileSizeInBytes int64,
		blockSizeInBytes int64,
	) (blockCount int64, err error)
	GetBlockSizeSumForUploadId(
		ctx context.Context,
		uploadId int64,
	) (totalSize int64, err error)
	GetById(
		ctx context.Context,
		id int64,
	) (ub *model.UploadBlock, err error)
	UpdateStatus(
		ctx context.Context,
		uploadId int64,
		blockId int64,
		status model.UploadBlockStatus,
	) error
	NextUnfinishedBlocks(
		ctx context.Context,
		uploadId int64,
		limit int,
	) (blockIds []int64, err error)
	MarkComplete(
		ctx context.Context,
		uploadId int64,
		blockId int64,
		checksum string,
		etag string,
	) error
	MarkError(
		ctx context.Context,
		uploadId int64,
		blockId int64,
		errorMsg string,
	) (retryCount int64, err error)
	GetCompletedBlocksForUploadId(
		ctx context.Context,
		uploadId int64,
	) (blocks []model.UploadBlock, err error)
}

type uploadBlockRepo struct {
	db *database.DB
}

func NewUploadBlockRepository(db *database.DB) UploadBlockRepository {
	return uploadBlockRepo{db: db}
}

func (ubr uploadBlockRepo) CreateUploadBlocks(
	ctx context.Context,
	uploadId int64,
	fileSizeInBytes int64,
	blockSizeInBytes int64,
) (int64, error) {
	if blockSizeInBytes <= 0 {
		return -1, fmt.Errorf("invalid block size, should be > 0")
	}

	totalBlocks := (fileSizeInBytes + blockSizeInBytes - 1) / (blockSizeInBytes)
	size, err := ubr.GetBlockSizeSumForUploadId(ctx, uploadId)

	if err != nil {
		return -1, err
	}
	if size == fileSizeInBytes {
		L.Info("Skipping creating upload blocks because blocks already exist")
		return 0, nil
	}
	// ASSUMPTION: file changes are handled some level up
	ARGS := 6
	row := "(" + strings.Repeat("?,", ARGS)[:ARGS*2-1] + ")"

	rows := make([]string, 0, totalBlocks)
	args := make([]interface{}, 0, totalBlocks*int64(ARGS))
	now := database.ToTimeStr(time.Now())
	for b := int64(0); b < totalBlocks; b++ {
		rows = append(rows, row)
		offset := b * blockSizeInBytes
		size := blockSizeInBytes
		if b == totalBlocks-1 {
			size = fileSizeInBytes - offset
		}
		args = append(args, uploadId, offset, size, model.UB_STATUS_QUEUED, now, now)
	}
	q := fmt.Sprintf(`INSERT INTO 
										upload_blocks 
										(upload_id, file_offset, size, status, created_at, updated_at) 
										VALUES %s`, strings.Join(rows, ","))
	res, err := ubr.db.D.ExecContext(ctx, q, args...)
	if err != nil {
		return -1, fmt.Errorf("CreateUploadBlocks: could not create upload blocks for upload id %d: %w", uploadId, err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return -1, fmt.Errorf("CreateUploadBlocks: could not get updated rows for create upload block for upload id %d: %w", uploadId, err)
	}
	return rowsAffected, nil
}

func (ubr uploadBlockRepo) GetBlockSizeSumForUploadId(
	ctx context.Context,
	uploadId int64,
) (int64, error) {
	var totalSize sql.NullInt64
	q := "SELECT SUM(size) from upload_blocks where upload_id = ?"

	err := ubr.db.D.QueryRowContext(ctx, q, uploadId).Scan(&totalSize)
	if err != nil {
		return -1, fmt.Errorf("could not get block count for upload id %d: %w", uploadId, err)
	}
	if totalSize.Valid {
		return totalSize.Int64, nil
	}
	return 0, nil
}

func (ubr uploadBlockRepo) UpdateStatus(
	ctx context.Context,
	uploadId int64,
	blockId int64,
	status model.UploadBlockStatus,
) error {
	q := "UPDATE upload_blocks SET status=?, completed_bytes=? where id=?"

	_, err := ubr.db.D.ExecContext(ctx, q, status, blockId)

	if err != nil {
		return fmt.Errorf("could not upload status for block id %d, status:%s : %w", blockId, status, err)
	}

	return nil
}

func (ubr uploadBlockRepo) MarkComplete(
	ctx context.Context,
	uploadId int64,
	blockId int64,
	checksum string,
	etag string,
) error {
	q := `UPDATE upload_blocks
			 SET checksum=?,
			 etag=?, 
			 status=?,
			 uploaded_at=?,
			 updated_at=?
			 WHERE id=? AND  upload_id=?`
	now := database.ToTimeStr(time.Now())
	result, err := ubr.db.D.ExecContext(ctx, q, checksum, etag, model.UB_STATUS_COMPLETE, now, now, blockId, uploadId)
	if err != nil {
		return fmt.Errorf("could not mark complete block with id %d: %w", blockId, err)
	}
	rows, _ := result.RowsAffected()

	if rows == 0 {
		return fmt.Errorf("no block found with id %d and upload_id %d", blockId, uploadId)
	}
	return nil
}

func (ubr uploadBlockRepo) MarkError(
	ctx context.Context,
	uploadId int64,
	blockId int64,
	errorMsg string,
) (retryCount int64, err error) {
	q := `UPDATE upload_blocks 
				SET status=?, 
				error_message=?, 
				error_count=error_count+1, 
				updated_at=? 
				WHERE id=? AND upload_id=?`
	_, err = ubr.db.D.ExecContext(ctx, q, model.UB_STATUS_ERROR, errorMsg, database.ToTimeStr(time.Now()), blockId, uploadId)
	if err != nil {
		return -1, fmt.Errorf("could not mark error block with id %d: %w", blockId, err)
	}
	q = `SELECT error_count FROM upload_blocks WHERE id=?`
	var errorCount int64
	err = ubr.db.D.QueryRowContext(ctx, q, blockId).Scan(&errorCount)
	if err != nil {
		return -1, fmt.Errorf("could not get error count for block with id %d:%w", blockId, err)
	}
	return errorCount, nil
}

func (ubr uploadBlockRepo) NextUnfinishedBlocks(
	ctx context.Context,
	uploadId int64,
	limit int,
) (blockIds []int64, err error) {
	q := "SELECT id from upload_blocks where upload_id=? and status IN (?,?) LIMIT ?"

	rows, err := ubr.db.D.QueryContext(ctx, q, uploadId, model.UB_STATUS_QUEUED, model.UB_STATUS_ERROR, limit)

	if err != nil {
		return blockIds, fmt.Errorf("could not get next blocks for upload id %d: %w", uploadId, err)
	}

	for rows.Next() {
		var id int64
		err = rows.Scan(&id)
		if err != nil {
			return blockIds, fmt.Errorf("could not scan rows to get next blocks for upload id:%d: %w", uploadId, err)
		}
		blockIds = append(blockIds, id)

	}
	defer rows.Close()

	return blockIds, nil
}

func (ubr uploadBlockRepo) GetById(ctx context.Context, id int64) (res *model.UploadBlock, err error) {
	q := `SELECT 
					id,
					upload_id,
					file_offset,
					size,
					status,
					etag,
					checksum,
					created_at,
					updated_at,
					uploaded_at,
					error_message,
					error_count 
				FROM upload_blocks WHERE id=?`

	row := ubr.db.D.QueryRow(q, id)
	var etagStr sql.NullString
	var checksumStr sql.NullString
	var createdAtStr string
	var updatedAtStr string
	var uploadedAtStr sql.NullString
	var errorMsg sql.NullString
	ub := model.UploadBlock{}
	err = row.Scan(
		&ub.Id,
		&ub.UploadId,
		&ub.FileOffset,
		&ub.Size,
		&ub.Status,
		&etagStr,
		&checksumStr,
		&createdAtStr,
		&updatedAtStr,
		&uploadedAtStr,
		&errorMsg,
		&ub.ErrorCount,
	)

	if err != nil {
		return nil, fmt.Errorf("could not get upload block for id %d:%w", id, err)
	}
	if etagStr.Valid {
		ub.Etag = etagStr.String
	}
	if checksumStr.Valid {
		ub.Checksum = checksumStr.String
	}
	if uploadedAtStr.Valid {
		ts := database.FromTimeStr(uploadedAtStr.String)
		ub.UploadedAt = &ts
	}
	ub.CreatedAt = database.FromTimeStr(createdAtStr)
	ub.UpdatedAt = database.FromTimeStr(updatedAtStr)
	return &ub, nil
}

func (ubr uploadBlockRepo) GetCompletedBlocksForUploadId(ctx context.Context, uploadId int64) ([]model.UploadBlock, error) {
	q := `SELECT 
					id,
					upload_id,
					file_offset,
					size,
					status,
					etag,
					checksum,
					created_at,
					updated_at,
					uploaded_at,
					error_message,
					error_count 
				FROM upload_blocks WHERE upload_id=? AND status=? ORDER BY id ASC`
	rows, err := ubr.db.D.QueryContext(ctx, q, uploadId, model.UB_STATUS_COMPLETE)
	var blocks []model.UploadBlock
	if err != nil {
		return blocks, fmt.Errorf("couldnt get completed blocks for upload id %d:%w", uploadId, err)
	}
	for rows.Next() {
		var etagStr sql.NullString
		var checksumStr sql.NullString
		var createdAtStr string
		var updatedAtStr string
		var uploadedAtStr sql.NullString
		var errorMsg sql.NullString
		ub := model.UploadBlock{}
		err = rows.Scan(
			&ub.Id,
			&ub.UploadId,
			&ub.FileOffset,
			&ub.Size,
			&ub.Status,
			&etagStr,
			&checksumStr,
			&createdAtStr,
			&updatedAtStr,
			&uploadedAtStr,
			&errorMsg,
			&ub.ErrorCount,
		)
		if err != nil {
			return nil, fmt.Errorf("could not scan upload block for id %d:%w", uploadId, err)
		}
		if etagStr.Valid {
			ub.Etag = etagStr.String
		}
		if checksumStr.Valid {
			ub.Checksum = checksumStr.String
		}
		if uploadedAtStr.Valid {
			ts := database.FromTimeStr(uploadedAtStr.String)
			ub.UploadedAt = &ts
		}
		ub.CreatedAt = database.FromTimeStr(createdAtStr)
		ub.UpdatedAt = database.FromTimeStr(updatedAtStr)
		blocks = append(blocks, ub)
	}
	return blocks, nil
}
