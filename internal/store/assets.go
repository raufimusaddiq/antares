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

// AssetFile is an owned asset with its bytes.
type AssetFile struct {
	Asset
	Content []byte
}

// ImageAssets returns uid's image assets with bytes, in the order of ids. Ownership and the
// image type are enforced in the query: an id that is unknown, someone else's, or not an
// image is ErrNoAsset, indistinguishably.
func ImageAssets(ctx context.Context, db *sql.DB, uid string, ids []string) ([]AssetFile, error) {
	out := make([]AssetFile, 0, len(ids))
	for _, id := range ids {
		var f AssetFile
		err := db.QueryRowContext(ctx, `SELECT id, media_type, size_bytes, created_at, content FROM assets
			WHERE id = ? AND user_id = ? AND media_type LIKE 'image/%'`, id, uid).
			Scan(&f.ID, &f.MediaType, &f.Size, &f.CreatedAt, &f.Content)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoAsset
		} else if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// AttachRunImages records which of uid's assets were sent with the run. The insert is
// restricted to assets uid owns, and the foreign key to a run uid owns.
func AttachRunImages(ctx context.Context, db *sql.DB, uid, runID string, assets []Asset) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, a := range assets {
		res, err := tx.ExecContext(ctx, `INSERT INTO run_images (run_id, user_id, position, asset_id, media_type)
			SELECT ?, ?, ?, id, media_type FROM assets WHERE id = ? AND user_id = ?`, runID, uid, i, a.ID, uid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoAsset
		}
	}
	return tx.Commit()
}

// RunImageSet is the images sent with one run; Live is false for an asset since deleted.
type RunImageSet struct {
	RunID  string
	Images []RunImage
}

type RunImage struct {
	AssetID, MediaType string
	Live               bool
}

// ChatRunImages lists, oldest first, the runs of uid's chat that carried images.
func ChatRunImages(ctx context.Context, db *sql.DB, uid, chatID string) ([]RunImageSet, error) {
	rows, err := db.QueryContext(ctx, `SELECT ri.run_id, ri.asset_id, ri.media_type, a.id IS NOT NULL
		FROM run_images ri
		JOIN runs r ON r.id = ri.run_id AND r.user_id = ri.user_id
		LEFT JOIN assets a ON a.id = ri.asset_id AND a.user_id = ri.user_id
		WHERE ri.user_id = ? AND r.chat_id = ?
		ORDER BY r.created_at, r.id, ri.position`, uid, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunImageSet
	for rows.Next() {
		var runID string
		var im RunImage
		if err := rows.Scan(&runID, &im.AssetID, &im.MediaType, &im.Live); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].RunID != runID {
			out = append(out, RunImageSet{RunID: runID})
		}
		out[len(out)-1].Images = append(out[len(out)-1].Images, im)
	}
	return out, rows.Err()
}
