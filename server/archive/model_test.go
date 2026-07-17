package archive

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsureTablesMigratesLegacyArchiveItems(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE "archive_item" (
		"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
		"task_id" integer NOT NULL DEFAULT 0,
		"bvid" text NOT NULL, "cid" integer NOT NULL, "page" integer NOT NULL,
		"title" text NOT NULL, "part" text NOT NULL, "owner" text NOT NULL,
		"file_path" text NOT NULL, "info_path" text NOT NULL, "cover_path" text NOT NULL,
		"danmaku_path" text NOT NULL, "status" text NOT NULL, "message" text NOT NULL DEFAULT '',
		"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
		"updated_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE("bvid", "cid")
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO "archive_item" ("task_id", "bvid", "cid", "page", "title", "part", "owner", "file_path", "info_path", "cover_path", "danmaku_path", "status") VALUES (9, 'BV1test', 101, 1, '标题', 'P1', 'UP', '/tmp/a.mp4', '/tmp/info.json', '', '', 'done')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureTables(db); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"archive_subject": 1, "archive_source_version": 1, "archive_part": 1} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
	var subjectID, versionID int64
	if err := db.QueryRow(`SELECT "subject_id", "version_id" FROM "archive_item" WHERE "id" = 1`).Scan(&subjectID, &versionID); err != nil {
		t.Fatal(err)
	}
	if subjectID == 0 || versionID == 0 {
		t.Fatalf("legacy item was not linked: subject=%d version=%d", subjectID, versionID)
	}
	if err := EnsureTables(db); err != nil {
		t.Fatal(err)
	}
	var partCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "archive_part"`).Scan(&partCount); err != nil {
		t.Fatal(err)
	}
	if partCount != 1 {
		t.Fatalf("migration is not idempotent, parts=%d", partCount)
	}
}
