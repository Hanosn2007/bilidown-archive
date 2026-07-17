package archive

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"bilidown/bilibili"
	"bilidown/util"
)

const archiveSchemaVersion = 1

type TimelineEvent struct {
	ID              int64  `json:"id"`
	SubjectID       int64  `json:"subjectId"`
	VersionID       int64  `json:"versionId"`
	PartID          int64  `json:"partId"`
	Kind            string `json:"kind"`
	Message         string `json:"message"`
	TargetSubjectID int64  `json:"targetSubjectId"`
	CreatedAt       string `json:"createdAt"`
}

type SourceVersion struct {
	ID           int64  `json:"id"`
	SubjectID    int64  `json:"subjectId"`
	Bvid         string `json:"bvid"`
	VersionNo    int    `json:"versionNo"`
	Availability string `json:"availability"`
	CreatedAt    string `json:"createdAt"`
}

type SubjectDetail struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	Owner     string          `json:"owner"`
	Status    string          `json:"status"`
	CreatedAt string          `json:"createdAt"`
	Versions  []SourceVersion `json:"versions"`
}

func ensureDomainTables(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS "archive_schema" (
			"version" integer NOT NULL,
			"applied_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS "archive_subject" (
			"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			"first_archive_order" integer NOT NULL DEFAULT 0,
			"title" text NOT NULL DEFAULT '',
			"owner" text NOT NULL DEFAULT '',
			"cover" text NOT NULL DEFAULT '',
			"status" text NOT NULL DEFAULT 'resolving',
			"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			"updated_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS "archive_source_version" (
			"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			"subject_id" integer NOT NULL,
			"source_key" text NOT NULL UNIQUE,
			"bvid" text NOT NULL DEFAULT '',
			"version_no" integer NOT NULL DEFAULT 1,
			"availability" text NOT NULL DEFAULT 'unknown',
			"metadata_json" text NOT NULL DEFAULT '{}',
			"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			"updated_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY("subject_id") REFERENCES "archive_subject"("id") ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS "archive_part" (
			"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			"version_id" integer NOT NULL,
			"archive_item_id" integer UNIQUE,
			"task_id" integer NOT NULL DEFAULT 0,
			"cid" integer NOT NULL DEFAULT 0,
			"page" integer NOT NULL DEFAULT 1,
			"title" text NOT NULL DEFAULT '',
			"duration" integer NOT NULL DEFAULT 0,
			"status" text NOT NULL DEFAULT 'resolving',
			"message" text NOT NULL DEFAULT '',
			"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			"updated_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY("version_id") REFERENCES "archive_source_version"("id") ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS "archive_container" (
			"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			"container_type" text NOT NULL,
			"source_key" text NOT NULL UNIQUE,
			"title" text NOT NULL DEFAULT '',
			"owner" text NOT NULL DEFAULT '',
			"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			"updated_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS "archive_container_member" (
			"container_id" integer NOT NULL,
			"subject_id" integer NOT NULL,
			"section_title" text NOT NULL DEFAULT '',
			"position" integer NOT NULL DEFAULT 0,
			PRIMARY KEY("container_id", "subject_id"),
			FOREIGN KEY("container_id") REFERENCES "archive_container"("id") ON DELETE CASCADE,
			FOREIGN KEY("subject_id") REFERENCES "archive_subject"("id") ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS "archive_event" (
			"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			"subject_id" integer NOT NULL,
			"version_id" integer NOT NULL DEFAULT 0,
			"part_id" integer NOT NULL DEFAULT 0,
			"kind" text NOT NULL,
			"message" text NOT NULL DEFAULT '',
			"target_subject_id" integer NOT NULL DEFAULT 0,
			"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY("subject_id") REFERENCES "archive_subject"("id") ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS "archive_event_created_idx" ON "archive_event"("id" DESC)`,
		`CREATE INDEX IF NOT EXISTS "archive_part_version_idx" ON "archive_part"("version_id", "page")`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if err := ensureArchiveItemColumn(db, "subject_id", `integer NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureArchiveItemColumn(db, "version_id", `integer NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureArchiveItemColumn(db, "duration", `integer NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureArchiveItemColumn(db, "availability", `text NOT NULL DEFAULT 'unknown'`); err != nil {
		return err
	}
	if err := ensureArchiveItemColumn(db, "metadata_json", `text NOT NULL DEFAULT '{}'`); err != nil {
		return err
	}
	if err := migrateLegacyItems(db); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO "archive_schema" ("version")
		SELECT ? WHERE NOT EXISTS (SELECT 1 FROM "archive_schema" WHERE "version" = ?)`, archiveSchemaVersion, archiveSchemaVersion)
	return err
}

func ensureArchiveItemColumn(db *sql.DB, name string, definition string) error {
	rows, err := db.Query(`PRAGMA table_info("archive_item")`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE "archive_item" ADD COLUMN "%s" %s`, name, definition))
	return err
}

func migrateLegacyItems(db *sql.DB) error {
	rows, err := db.Query(`SELECT "id", "task_id", "bvid", "cid", "page", "title", "part", "owner", "status", "message", "created_at"
		FROM "archive_item" WHERE "subject_id" = 0 ORDER BY "id"`)
	if err != nil {
		return err
	}
	type legacy struct {
		id, taskID int64
		bvid       string
		cid, page  int
		title      string
		part       string
		owner      string
		status     string
		message    string
		createdAt  string
	}
	items := []legacy{}
	for rows.Next() {
		item := legacy{}
		if err := rows.Scan(&item.id, &item.taskID, &item.bvid, &item.cid, &item.page, &item.title, &item.part, &item.owner, &item.status, &item.message, &item.createdAt); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		sourceKey := sourceKeyFor(item.bvid, item.id)
		subjectID, versionID, err := ensureSubjectVersion(db, sourceKey, item.bvid, item.title, item.owner, "", item.status, "{}", item.id)
		if err != nil {
			return err
		}
		result, err := db.Exec(`INSERT OR IGNORE INTO "archive_part"
			("version_id", "archive_item_id", "task_id", "cid", "page", "title", "status", "message", "created_at", "updated_at")
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, versionID, item.id, item.taskID, item.cid, item.page, item.part, item.status, item.message, item.createdAt)
		if err != nil {
			return err
		}
		partID, _ := result.LastInsertId()
		if _, err := db.Exec(`UPDATE "archive_item" SET "subject_id" = ?, "version_id" = ? WHERE "id" = ?`, subjectID, versionID, item.id); err != nil {
			return err
		}
		if partID > 0 {
			_, _ = db.Exec(`INSERT INTO "archive_event" ("subject_id", "version_id", "part_id", "kind", "message", "created_at") VALUES (?, ?, ?, 'migrated', '从旧归档记录迁移', ?)`, subjectID, versionID, partID, item.createdAt)
		}
	}
	return nil
}

func sourceKeyFor(bvid string, fallbackID int64) string {
	if strings.TrimSpace(bvid) != "" {
		return "bvid:" + strings.TrimSpace(bvid)
	}
	return fmt.Sprintf("legacy:%d", fallbackID)
}

func ensureSubjectVersion(db *sql.DB, sourceKey string, bvid string, title string, owner string, cover string, status string, metadataJSON string, order int64) (int64, int64, error) {
	var subjectID, versionID int64
	err := db.QueryRow(`SELECT "subject_id", "id" FROM "archive_source_version" WHERE "source_key" = ?`, sourceKey).Scan(&subjectID, &versionID)
	if err == nil {
		_, _ = db.Exec(`UPDATE "archive_subject" SET "title" = CASE WHEN ? <> '' THEN ? ELSE "title" END,
			"owner" = CASE WHEN ? <> '' THEN ? ELSE "owner" END, "cover" = CASE WHEN ? <> '' THEN ? ELSE "cover" END,
			"status" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, title, title, owner, owner, cover, cover, status, subjectID)
		_, _ = db.Exec(`UPDATE "archive_source_version" SET "bvid" = CASE WHEN ? <> '' THEN ? ELSE "bvid" END,
			"metadata_json" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, bvid, bvid, metadataJSON, versionID)
		return subjectID, versionID, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, err
	}
	result, err := db.Exec(`INSERT INTO "archive_subject" ("first_archive_order", "title", "owner", "cover", "status") VALUES (?, ?, ?, ?, ?)`, order, title, owner, cover, status)
	if err != nil {
		return 0, 0, err
	}
	subjectID, err = result.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	result, err = db.Exec(`INSERT INTO "archive_source_version" ("subject_id", "source_key", "bvid", "availability", "metadata_json") VALUES (?, ?, ?, 'unknown', ?)`, subjectID, sourceKey, bvid, metadataJSON)
	if err != nil {
		return 0, 0, err
	}
	versionID, err = result.LastInsertId()
	if err == nil {
		_, _ = db.Exec(`INSERT INTO "archive_event" ("subject_id", "version_id", "kind", "message") VALUES (?, ?, 'discovered', '首次发现视频')`, subjectID, versionID)
	}
	return subjectID, versionID, err
}

func registerFavoriteSnapshot(db *sql.DB, mediaID int, fav bilibili.FavoriteItem) (int64, int64, string, error) {
	metadata, err := json.Marshal(fav)
	if err != nil {
		return 0, 0, "", err
	}
	sourceKey := "bvid:" + strings.TrimSpace(fav.Bvid)
	if strings.TrimSpace(fav.Bvid) == "" {
		sourceKey = fmt.Sprintf("favorite:%d:%d", mediaID, fav.ID)
	}
	subjectID, versionID, err := ensureSubjectVersion(db, sourceKey, fav.Bvid, fav.Title, fav.Upper.Name, fav.Cover, "resolving", string(metadata), fav.MTime)
	return subjectID, versionID, sourceKey, err
}

func updateVersionAvailability(db *sql.DB, subjectID int64, versionID int64, availability string, status string, message string, eventKind string) error {
	if _, err := db.Exec(`UPDATE "archive_source_version" SET "availability" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, availability, versionID); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE "archive_subject" SET "status" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, status, subjectID); err != nil {
		return err
	}
	if eventKind != "" {
		_, err := db.Exec(`INSERT INTO "archive_event" ("subject_id", "version_id", "kind", "message") VALUES (?, ?, ?, ?)`, subjectID, versionID, eventKind, message)
		return err
	}
	return nil
}

func syncUGCContainer(db *sql.DB, subjectID int64, videoInfo *bilibili.VideoInfo) error {
	if videoInfo == nil || strings.TrimSpace(videoInfo.UgcSeason.Title) == "" || len(videoInfo.UgcSeason.Sections) == 0 {
		return nil
	}
	position := 0
	currentPosition := 0
	sectionTitle := ""
	for _, section := range videoInfo.UgcSeason.Sections {
		for _, episode := range section.Episodes {
			position++
			if episode.Bvid == videoInfo.Bvid {
				currentPosition = position
				sectionTitle = section.Title
			}
		}
	}
	if currentPosition == 0 {
		return nil
	}
	sourceKey := fmt.Sprintf("ugc:%d:%s", videoInfo.Owner.Mid, strings.TrimSpace(videoInfo.UgcSeason.Title))
	_, err := db.Exec(`INSERT INTO "archive_container" ("container_type", "source_key", "title", "owner") VALUES ('formal_collection', ?, ?, ?)
		ON CONFLICT("source_key") DO UPDATE SET "title" = excluded."title", "owner" = excluded."owner", "updated_at" = CURRENT_TIMESTAMP`, sourceKey, videoInfo.UgcSeason.Title, videoInfo.Owner.Name)
	if err != nil {
		return err
	}
	var containerID int64
	if err := db.QueryRow(`SELECT "id" FROM "archive_container" WHERE "source_key" = ?`, sourceKey).Scan(&containerID); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO "archive_container_member" ("container_id", "subject_id", "section_title", "position") VALUES (?, ?, ?, ?)
		ON CONFLICT("container_id", "subject_id") DO UPDATE SET "section_title" = excluded."section_title", "position" = excluded."position"`, containerID, subjectID, sectionTitle, currentPosition)
	return err
}

// LinkSourceSubject 将被确认是“换源”的新主体并入首次归档主体，保持首次归档顺序。
func LinkSourceSubject(db *sql.DB, sourceSubjectID int64, targetSubjectID int64) error {
	if sourceSubjectID <= 0 || targetSubjectID <= 0 || sourceSubjectID == targetSubjectID {
		return fmt.Errorf("主体参数无效")
	}
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	var sourceTitle, targetTitle string
	if err := tx.QueryRow(`SELECT "title" FROM "archive_subject" WHERE "id" = ?`, sourceSubjectID).Scan(&sourceTitle); err != nil {
		return rollback(err)
	}
	if err := tx.QueryRow(`SELECT "title" FROM "archive_subject" WHERE "id" = ?`, targetSubjectID).Scan(&targetTitle); err != nil {
		return rollback(err)
	}
	var maxVersion int
	_ = tx.QueryRow(`SELECT COALESCE(MAX("version_no"), 0) FROM "archive_source_version" WHERE "subject_id" = ?`, targetSubjectID).Scan(&maxVersion)
	rows, err := tx.Query(`SELECT "id" FROM "archive_source_version" WHERE "subject_id" = ? ORDER BY "version_no", "id"`, sourceSubjectID)
	if err != nil {
		return rollback(err)
	}
	versionIDs := []int64{}
	for rows.Next() {
		var versionID int64
		if err := rows.Scan(&versionID); err != nil {
			rows.Close()
			return rollback(err)
		}
		versionIDs = append(versionIDs, versionID)
	}
	rows.Close()
	for i, versionID := range versionIDs {
		if _, err := tx.Exec(`UPDATE "archive_source_version" SET "subject_id" = ?, "version_no" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, targetSubjectID, maxVersion+i+1, versionID); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Exec(`UPDATE "archive_item" SET "subject_id" = ? WHERE "subject_id" = ?`, targetSubjectID, sourceSubjectID); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`UPDATE "archive_event" SET "subject_id" = ?, "target_subject_id" = ? WHERE "subject_id" = ?`, targetSubjectID, targetSubjectID, sourceSubjectID); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO "archive_container_member" ("container_id", "subject_id", "section_title", "position") SELECT "container_id", ?, "section_title", "position" FROM "archive_container_member" WHERE "subject_id" = ?`, targetSubjectID, sourceSubjectID); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`DELETE FROM "archive_container_member" WHERE "subject_id" = ?`, sourceSubjectID); err != nil {
		return rollback(err)
	}
	message := fmt.Sprintf("发现新来源“%s”，已并入首次归档主体“%s”", sourceTitle, targetTitle)
	if _, err := tx.Exec(`INSERT INTO "archive_event" ("subject_id", "kind", "message", "target_subject_id") VALUES (?, 'version_update_jump', ?, ?)`, targetSubjectID, message, targetSubjectID); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`DELETE FROM "archive_subject" WHERE "id" = ?`, sourceSubjectID); err != nil {
		return rollback(err)
	}
	return tx.Commit()
}

func GetSubjectDetail(db *sql.DB, subjectID int64) (*SubjectDetail, error) {
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	detail := &SubjectDetail{ID: subjectID, Versions: []SourceVersion{}}
	if err := db.QueryRow(`SELECT "title", "owner", "status", "created_at" FROM "archive_subject" WHERE "id" = ?`, subjectID).Scan(&detail.Title, &detail.Owner, &detail.Status, &detail.CreatedAt); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT "id", "subject_id", "bvid", "version_no", "availability", "created_at" FROM "archive_source_version" WHERE "subject_id" = ? ORDER BY "version_no", "id"`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		version := SourceVersion{}
		if err := rows.Scan(&version.ID, &version.SubjectID, &version.Bvid, &version.VersionNo, &version.Availability, &version.CreatedAt); err != nil {
			return nil, err
		}
		detail.Versions = append(detail.Versions, version)
	}
	return detail, rows.Err()
}

func ListTimelineEvents(db *sql.DB, after int64, limit int) ([]TimelineEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 160
	}
	util.SqliteLock.Lock()
	rows, err := db.Query(`SELECT "id", "subject_id", "version_id", "part_id", "kind", "message", "target_subject_id", "created_at"
		FROM "archive_event" WHERE "id" > ? ORDER BY "id" DESC LIMIT ?`, after, limit)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []TimelineEvent{}
	for rows.Next() {
		event := TimelineEvent{}
		if err := rows.Scan(&event.ID, &event.SubjectID, &event.VersionID, &event.PartID, &event.Kind, &event.Message, &event.TargetSubjectID, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func addEvent(db *sql.DB, subjectID int64, versionID int64, partID int64, kind string, message string) {
	if subjectID <= 0 || strings.TrimSpace(kind) == "" {
		return
	}
	_, _ = db.Exec(`INSERT INTO "archive_event" ("subject_id", "version_id", "part_id", "kind", "message", "created_at") VALUES (?, ?, ?, ?, ?, ?)`, subjectID, versionID, partID, kind, message, time.Now().Format("2006-01-02 15:04:05"))
}
