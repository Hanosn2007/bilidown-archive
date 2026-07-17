package archive

import (
	"database/sql"

	"bilidown/util"
)

// GetItem returns one archive item with paths normalized to the current archive folder.
func GetItem(db *sql.DB, id int64) (*Item, error) {
	item := Item{}
	util.SqliteLock.Lock()
	err := db.QueryRow(`SELECT
		"id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "status", "message",
		"created_at", "updated_at"
	FROM "archive_item" WHERE "id" = ?`, id).Scan(
		&item.ID, &item.TaskID, &item.Bvid, &item.Cid, &item.Page, &item.Title, &item.Part, &item.Owner,
		&item.FilePath, &item.InfoPath, &item.CoverPath, &item.DanmakuPath, &item.Status, &item.Message,
		&item.CreatedAt, &item.UpdatedAt,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}
	archiveFolder, _ := util.GetArchiveFolder(db)
	normalizeItemPaths(&item, archiveFolder)
	return &item, nil
}
