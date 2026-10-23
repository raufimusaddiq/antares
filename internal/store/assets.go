package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoAsset is returned for an asset that does not exist OR belongs to someone else.
var ErrNoAsset = errors.New("store: no such asset")

// ErrAssetQuota means the upload would take the owner past their staged-audio budget.
var ErrAssetQuota = errors.New("store: staged audio quota exceeded")

// Asset is the metadata of one staged upload; the bytes are read separately.
type Asset struct {
	ID        string
	MediaType string
	Size      int64
	CreatedAt string
}

// CreateAsset stores content for uid in one statement: the quota check and the insert
// cannot be split by a concurrent upload, and a failure leaves no row.
func CreateAsset(ctx context.Context, db *sql.DB, uid, mediaType string, content []byte, userQuota int64) (Asset, error) {
	a := Asset{ID: NewID(), MediaType: mediaType, Size: int64(len(content)), CreatedAt: ts(time.Now())}
	res, err := db.ExecContext(ctx, `INSERT INTO assets (id, user_id, media_type, size_bytes, created_at, content)
		SELECT ?, ?, ?, ?, ?, ?
		WHERE (SELECT COALESCE(SUM(size_bytes), 0) FROM assets WHERE user_id = ?) + ? <= ?`,
		a.ID, uid, mediaType, a.Size, a.CreatedAt, content, uid, a.Size, userQuota)
	if err != nil {
		return Asset{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Asset{}, ErrAssetQuota
	}
	return a, nil
}

// GetAsset returns uid's asset with its bytes. Someone else's asset is ErrNoAsset.
func GetAsset(ctx context.Context, db *sql.DB, uid, id string) (Asset, []byte, error) {
	var a Asset
	var content []byte
	err := db.QueryRowContext(ctx, `SELECT id, media_type, size_bytes, created_at, content FROM assets WHERE id = ? AND user_id = ?`, id, uid).
		Scan(&a.ID, &a.MediaType, &a.Size, &a.CreatedAt, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, nil, ErrNoAsset
	}
	return a, content, err
}

// DeleteAsset removes uid's asset. Someone else's asset is ErrNoAsset.
func DeleteAsset(ctx context.Context, db *sql.DB, uid, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM assets WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoAsset
	}
	return nil
}
