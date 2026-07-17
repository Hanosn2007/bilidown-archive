package archive

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"bilidown/bilibili"
	"bilidown/common"
	"bilidown/task"
	"bilidown/util"
)

type Settings struct {
	Enabled          bool   `json:"enabled"`
	FavMediaID       int    `json:"favMediaId"`
	IntervalMinutes  int    `json:"intervalMinutes"`
	ArchiveFolder    string `json:"archiveFolder"`
	DownloadAllPages bool   `json:"downloadAllPages"`
	DownloadType     string `json:"downloadType"`
	Format           string `json:"format"`
	PreferredCodec   int    `json:"preferredCodec"`
	PreferHiResAudio bool   `json:"preferHiResAudio"`
}

type Item struct {
	ID           int64  `json:"id"`
	SubjectID    int64  `json:"subjectId"`
	VersionID    int64  `json:"versionId"`
	TaskID       int64  `json:"taskId"`
	Bvid         string `json:"bvid"`
	Cid          int    `json:"cid"`
	Page         int    `json:"page"`
	Title        string `json:"title"`
	Part         string `json:"part"`
	Owner        string `json:"owner"`
	FilePath     string `json:"filePath"`
	InfoPath     string `json:"infoPath"`
	CoverPath    string `json:"coverPath"`
	DanmakuPath  string `json:"danmakuPath"`
	Duration     int    `json:"duration"`
	Availability string `json:"availability"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type ScanResult struct {
	FavMediaID int      `json:"favMediaId"`
	Found      int      `json:"found"`
	Created    int      `json:"created"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Messages   []string `json:"messages"`
}

type BatchResult struct {
	Deleted  int      `json:"deleted"`
	Retried  int      `json:"retried"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Messages []string `json:"messages"`
}

var monitorRunning bool
var monitorStop = make(chan struct{})
var monitorWake = make(chan struct{}, 1)
var scanExecutionMux sync.Mutex

func EnsureTables(db *sql.DB) error {
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS "archive_item" (
		"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
		"task_id" integer NOT NULL DEFAULT 0,
		"bvid" text NOT NULL,
		"cid" integer NOT NULL,
		"page" integer NOT NULL,
		"title" text NOT NULL,
		"part" text NOT NULL,
		"owner" text NOT NULL,
		"file_path" text NOT NULL,
		"info_path" text NOT NULL,
		"cover_path" text NOT NULL,
		"danmaku_path" text NOT NULL,
		"status" text NOT NULL,
		"message" text NOT NULL DEFAULT '',
		"created_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
		"updated_at" text NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE("bvid", "cid")
	)`)
	if err != nil {
		return err
	}
	return ensureDomainTables(db)
}

func GetSettings(db *sql.DB) (Settings, error) {
	settings := Settings{
		Enabled:          false,
		IntervalMinutes:  10,
		DownloadAllPages: true,
		DownloadType:     "merge",
		Format:           "highest",
		PreferredCodec:   12,
		PreferHiResAudio: true,
	}
	fields, err := util.GetFields(db,
		"archive_folder",
		"archive_monitor_enabled",
		"archive_fav_media_id",
		"archive_scan_interval_minutes",
		"archive_download_all_pages",
		"archive_download_type",
		"archive_format",
		"archive_preferred_codec",
		"archive_prefer_hires_audio",
	)
	if err != nil {
		return settings, err
	}
	if strings.TrimSpace(fields["archive_folder"]) != "" {
		settings.ArchiveFolder = strings.TrimSpace(fields["archive_folder"])
	} else if folder, err := util.GetArchiveFolder(db); err == nil {
		settings.ArchiveFolder = folder
	}
	settings.Enabled = fields["archive_monitor_enabled"] == "1"
	if v, err := strconv.Atoi(fields["archive_fav_media_id"]); err == nil {
		settings.FavMediaID = v
	}
	if v, err := strconv.Atoi(fields["archive_scan_interval_minutes"]); err == nil && v > 0 {
		settings.IntervalMinutes = v
	}
	if fields["archive_download_all_pages"] == "0" {
		settings.DownloadAllPages = false
	}
	if v := fields["archive_download_type"]; v == "audio" || v == "video" || v == "merge" {
		settings.DownloadType = v
	}
	if v := fields["archive_format"]; v != "" {
		settings.Format = v
	}
	if v, err := strconv.Atoi(fields["archive_preferred_codec"]); err == nil && (v == 12 || v == 7 || v == 13) {
		settings.PreferredCodec = v
	}
	if fields["archive_prefer_hires_audio"] == "0" {
		settings.PreferHiResAudio = false
	}
	return settings, nil
}

func SaveSettings(db *sql.DB, settings Settings) error {
	if settings.IntervalMinutes <= 0 {
		settings.IntervalMinutes = 10
	}
	settings.ArchiveFolder = strings.TrimSpace(settings.ArchiveFolder)
	if settings.ArchiveFolder == "" {
		folder, err := util.GetDefaultArchiveFolder()
		if err != nil {
			return err
		}
		settings.ArchiveFolder = folder
	}
	if err := util.SaveArchiveFolder(db, settings.ArchiveFolder); err != nil {
		return err
	}
	if settings.DownloadType != "audio" && settings.DownloadType != "video" && settings.DownloadType != "merge" {
		settings.DownloadType = "merge"
	}
	if settings.Format == "" {
		settings.Format = "highest"
	}
	if settings.PreferredCodec != 12 && settings.PreferredCodec != 7 && settings.PreferredCodec != 13 {
		settings.PreferredCodec = 12
	}
	enabled := "0"
	if settings.Enabled {
		enabled = "1"
	}
	allPages := "0"
	if settings.DownloadAllPages {
		allPages = "1"
	}
	hiResAudio := "0"
	if settings.PreferHiResAudio {
		hiResAudio = "1"
	}
	return util.SaveFields(db, [][2]string{
		{"archive_folder", settings.ArchiveFolder},
		{"archive_monitor_enabled", enabled},
		{"archive_fav_media_id", strconv.Itoa(settings.FavMediaID)},
		{"archive_scan_interval_minutes", strconv.Itoa(settings.IntervalMinutes)},
		{"archive_download_all_pages", allPages},
		{"archive_download_type", settings.DownloadType},
		{"archive_format", settings.Format},
		{"archive_preferred_codec", strconv.Itoa(settings.PreferredCodec)},
		{"archive_prefer_hires_audio", hiResAudio},
	})
}

func ListItems(db *sql.DB, query string, limit int) ([]Item, error) {
	if limit <= 0 || limit > 500 {
		limit = 120
	}
	args := []any{}
	where := ""
	if strings.TrimSpace(query) != "" {
		like := "%" + strings.TrimSpace(query) + "%"
		where = `WHERE "bvid" LIKE ? OR "title" LIKE ? OR "part" LIKE ? OR "owner" LIKE ?`
		args = append(args, like, like, like, like)
	}
	args = append(args, limit)
	util.SqliteLock.Lock()
	rows, err := db.Query(`SELECT
		"id", "subject_id", "version_id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "duration", "availability", "status", "message",
		"created_at", "updated_at"
	FROM "archive_item" `+where+` ORDER BY "id" DESC LIMIT ?`, args...)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	archiveFolder, _ := util.GetArchiveFolder(db)
	for rows.Next() {
		item := Item{}
		if err := rows.Scan(
			&item.ID, &item.SubjectID, &item.VersionID, &item.TaskID, &item.Bvid, &item.Cid, &item.Page, &item.Title, &item.Part, &item.Owner,
			&item.FilePath, &item.InfoPath, &item.CoverPath, &item.DanmakuPath, &item.Duration, &item.Availability, &item.Status, &item.Message,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		normalizeItemPaths(&item, archiveFolder)
		items = append(items, item)
	}
	return items, nil
}

func DeleteItems(db *sql.DB, ids []int64) (*BatchResult, error) {
	result := &BatchResult{}
	items, err := getItemsByIDs(db, ids)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		if item.TaskID > 0 && (item.Status == "resolving" || item.Status == "waiting" || item.Status == "running") {
			if !task.CancelAndWait(item.TaskID, 10*time.Second) {
				// 任务可能尚未启动或已自然退出；Start 会在发现数据库记录已删除后停止。
				result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 未发现活动下载进程，继续清理", item.Bvid, item.Cid))
			}
		}
		if err := deleteItemFiles(item); err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 删除文件失败: %v", item.Bvid, item.Cid, err))
			continue
		}
		if err := deleteExistingItem(db, &item); err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 删除记录失败: %v", item.Bvid, item.Cid, err))
			continue
		}
		result.Deleted++
	}
	return result, nil
}

func DeletePreviewItems(db *sql.DB, ids []int64) (*BatchResult, error) {
	result := &BatchResult{}
	items, err := getItemsByIDs(db, ids)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		deleted, err := deletePreviewFile(item.FilePath)
		if err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 删除预览失败: %v", item.Bvid, item.Cid, err))
			continue
		}
		if deleted {
			result.Deleted++
		} else {
			result.Skipped++
		}
	}
	return result, nil
}

func ClearPreviewCache() (*BatchResult, error) {
	result := &BatchResult{}
	download, err := util.GetDefaultDownloadFolder()
	if err != nil {
		return result, err
	}
	previewDir := filepath.Join(download, "_preview")
	entries, err := os.ReadDir(previewDir)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(previewDir, entry.Name())
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s 删除失败: %v", entry.Name(), err))
			continue
		}
		result.Deleted++
	}
	removeEmptyDir(previewDir)
	return result, nil
}

func RetryItems(db *sql.DB, ids []int64) (*BatchResult, error) {
	result := &BatchResult{}
	settings, err := GetSettings(db)
	if err != nil {
		return result, err
	}
	sessdata, err := bilibili.GetSessdata(db)
	if err != nil || sessdata == "" {
		return result, fmt.Errorf("请先扫码登录")
	}
	client := &bilibili.BiliClient{SESSDATA: sessdata}
	items, err := getItemsByIDs(db, ids)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		if item.Status != "error" && item.Status != "unavailable" {
			result.Skipped++
			continue
		}
		videoInfo, err := client.GetVideoInfo(item.Bvid)
		if err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s 解析失败: %v", item.Bvid, err))
			continue
		}
		page, ok := findPageByCID(videoInfo.Pages, item.Cid)
		if !ok && len(videoInfo.Pages) > 0 && item.TaskID == 0 {
			page = videoInfo.Pages[0]
			ok = true
		}
		if !ok {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 未找到分P", item.Bvid, item.Cid))
			continue
		}
		if err := createPageTask(db, client, settings, videoInfo, page, &item); err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 重新入队失败: %v", item.Bvid, item.Cid, err))
			continue
		}
		result.Retried++
	}
	return result, nil
}

func ScanFavorite(db *sql.DB) (*ScanResult, error) {
	scanExecutionMux.Lock()
	defer scanExecutionMux.Unlock()
	settings, err := GetSettings(db)
	if err != nil {
		return nil, err
	}
	result := &ScanResult{FavMediaID: settings.FavMediaID}
	if settings.FavMediaID <= 0 {
		return result, fmt.Errorf("请先设置收藏夹 media_id")
	}
	sessdata, err := bilibili.GetSessdata(db)
	if err != nil || sessdata == "" {
		return result, fmt.Errorf("请先扫码登录")
	}
	client := &bilibili.BiliClient{SESSDATA: sessdata}
	favList, err := client.GetFavlist(settings.FavMediaID)
	if err != nil {
		return result, err
	}
	result.Found = len(*favList)
	for _, fav := range *favList {
		created, skipped, failed, messages := createFromFavorite(db, client, settings, fav)
		result.Created += created
		result.Skipped += skipped
		result.Failed += failed
		result.Messages = append(result.Messages, messages...)
	}
	return result, nil
}

func createFromFavorite(db *sql.DB, client *bilibili.BiliClient, settings Settings, fav bilibili.FavoriteItem) (created int, skipped int, failed int, messages []string) {
	queued := 0
	hasCompleted := false
	util.SqliteLock.Lock()
	subjectID, versionID, sourceKey, err := registerFavoriteSnapshot(db, settings.FavMediaID, fav)
	util.SqliteLock.Unlock()
	if err != nil {
		return 0, 0, 1, []string{fmt.Sprintf("%s 保存收藏夹快照失败: %v", fav.Bvid, err)}
	}
	metadata, _ := json.Marshal(fav)
	shell, wasCreated, err := ensureFavoriteShell(db, client, settings, fav, subjectID, versionID, sourceKey, string(metadata))
	if err != nil {
		return 0, 0, 1, []string{fmt.Sprintf("%s 建立归档占位失败: %v", fav.Bvid, err)}
	}
	if wasCreated {
		created++
	}
	videoInfo, err := client.GetVideoInfo(fav.Bvid)
	if err != nil {
		if shell.Status == "done" {
			message := fmt.Sprintf("源站当前不可用，本地归档仍可播放: %v", err)
			availability := "unknown"
			eventKind := "source_check_error"
			var apiErr *bilibili.APIError
			if errors.As(err, &apiErr) && apiErr.ContentUnavailable() {
				availability = "unavailable"
				eventKind = "source_unavailable_after_archive"
				message = fmt.Sprintf("源站后来不可用，本地归档仍保留: %s", apiErr.Message)
			}
			util.SqliteLock.Lock()
			_ = updateVersionAvailability(db, subjectID, versionID, availability, "done", message, eventKind)
			util.SqliteLock.Unlock()
			return created, skipped + 1, 0, []string{fmt.Sprintf("%s %s", fav.Bvid, message)}
		}
		status := "error"
		availability := "unknown"
		eventKind := "metadata_error"
		message := fmt.Sprintf("读取视频详情失败，可稍后重试: %v", err)
		var apiErr *bilibili.APIError
		if errors.As(err, &apiErr) && apiErr.ContentUnavailable() {
			status = "unavailable"
			availability = "unavailable"
			eventKind = "unavailable_before_backup"
			message = fmt.Sprintf("视频在完成备份前已不可用: %s", apiErr.Message)
		}
		_ = updateArchiveItemState(db, shell.ID, status, availability, message)
		util.SqliteLock.Lock()
		_ = updateVersionAvailability(db, subjectID, versionID, availability, status, message, eventKind)
		util.SqliteLock.Unlock()
		return created, skipped, 1, []string{fmt.Sprintf("%s %s", fav.Bvid, message)}
	}
	util.SqliteLock.Lock()
	containerErr := syncUGCContainer(db, subjectID, videoInfo)
	util.SqliteLock.Unlock()
	if containerErr != nil {
		messages = append(messages, fmt.Sprintf("%s 合集关系保存失败: %v", fav.Bvid, containerErr))
	}
	pages := videoInfo.Pages
	if !settings.DownloadAllPages && len(pages) > 1 {
		pages = pages[:1]
	}
	for _, page := range pages {
		existing, err := getExistingItem(db, videoInfo.Bvid, page.Cid)
		if err != nil {
			failed++
			messages = append(messages, fmt.Sprintf("%s/%d 检查失败: %v", videoInfo.Bvid, page.Cid, err))
			continue
		}
		if existing != nil && (existing.Status == "done" || existing.Status == "running" || existing.Status == "waiting") {
			if existing.Status == "done" {
				hasCompleted = true
			}
			skipped++
			continue
		}
		if existing == nil {
			existing, err = ensurePagePlaceholder(db, videoInfo, page, subjectID, versionID, string(metadata))
			if err != nil {
				failed++
				messages = append(messages, fmt.Sprintf("%s/%d 建立下载占位失败: %v", videoInfo.Bvid, page.Cid, err))
				continue
			}
			created++
		}
		if err := createPageTask(db, client, settings, videoInfo, page, existing); err != nil {
			failed++
			messages = append(messages, fmt.Sprintf("%s/%d 入队失败: %v", videoInfo.Bvid, page.Cid, err))
			continue
		}
		queued++
	}
	if queued > 0 || hasCompleted {
		status := "downloading"
		if queued == 0 && hasCompleted {
			status = "done"
		}
		util.SqliteLock.Lock()
		_ = updateVersionAvailability(db, subjectID, versionID, "available", status, "", "")
		util.SqliteLock.Unlock()
	}
	return
}

func createPageTask(db *sql.DB, client *bilibili.BiliClient, settings Settings, videoInfo *bilibili.VideoInfo, page bilibili.Page, item *Item) error {
	if item == nil {
		return fmt.Errorf("归档占位记录不存在")
	}
	_ = updateArchiveItemState(db, item.ID, "resolving", "available", "正在解析播放地址")
	playInfo, err := client.GetPlayInfo(videoInfo.Bvid, page.Cid)
	if err != nil {
		return markPlayInfoFailure(db, item, videoInfo, err)
	}
	if playInfo == nil || playInfo.Dash == nil {
		return markPlayInfoFailure(db, item, videoInfo, fmt.Errorf("播放流信息为空"))
	}
	var format common.MediaFormat
	videoURL := ""
	if settings.DownloadType != "audio" {
		if len(playInfo.Dash.Video) > 0 {
			format, err = chooseFormat(playInfo, settings.Format)
			if err != nil {
				return markPlayInfoFailure(db, item, videoInfo, err)
			}
			videoURL, err = getVideoURL(playInfo.Dash.Video, format, settings.PreferredCodec)
			if err != nil {
				return markPlayInfoFailure(db, item, videoInfo, err)
			}
		}
	}
	audioURL := ""
	if settings.DownloadType != "video" {
		audioURL = getAudioURL(playInfo.Dash, settings.PreferHiResAudio)
	}
	downloadType := effectiveDownloadType(settings.DownloadType, audioURL, videoURL)
	if downloadType == "" {
		return markPlayInfoFailure(db, item, videoInfo, fmt.Errorf("没有可下载的音频或视频流"))
	}
	folder, err := util.GetArchiveFolder(db)
	if err != nil {
		return err
	}
	title := buildTaskTitle(videoInfo, page, format, playInfo.Dash.Duration)
	newTask := task.Task{TaskInDB: task.TaskInDB{TaskInitOption: task.TaskInitOption{
		Bvid: videoInfo.Bvid, Cid: page.Cid, Format: format, Title: util.FilterFileName(title),
		Owner: videoInfo.Owner.Name, Cover: videoInfo.Pic, Folder: folder, Status: "waiting",
		Audio: audioURL, Video: videoURL, Duration: playInfo.Dash.Duration, DownloadType: downloadType,
	}}}
	if err := prepareArchiveTask(db, item, &newTask); err != nil {
		_ = updateArchiveItemState(db, item.ID, "error", "available", err.Error())
		return err
	}
	filePath := newTask.FilePath()
	infoPath, coverPath, danmakuPath, err := writeSidecars(client, folder, videoInfo, page, playInfo, settings, filePath)
	if err != nil {
		_ = newTask.UpdateStatus(db, "error", err)
		_ = updateArchiveItemState(db, item.ID, "error", "available", fmt.Sprintf("写入归档信息失败: %v", err))
		return err
	}
	if err := attachTaskToItem(db, item.ID, &newTask, page, playInfo.Dash.Duration, filePath, infoPath, coverPath, danmakuPath); err != nil {
		_ = newTask.UpdateStatus(db, "error", err)
		return err
	}
	go newTask.Start()
	return nil
}

func markPlayInfoFailure(db *sql.DB, item *Item, videoInfo *bilibili.VideoInfo, cause error) error {
	status := "error"
	availability := "available"
	eventKind := "play_info_error"
	message := fmt.Sprintf("解析播放地址失败，可稍后重试: %v", cause)
	var apiErr *bilibili.APIError
	if errors.As(cause, &apiErr) && apiErr.ContentUnavailable() {
		status = "unavailable"
		availability = "unavailable"
		eventKind = "unavailable_before_backup"
		message = fmt.Sprintf("视频在下载前已不可用: %s", apiErr.Message)
	} else if restriction := accessRestrictionHint(videoInfo, cause); restriction != "" {
		availability = "restricted"
		eventKind = "access_restricted"
		message = fmt.Sprintf("检测到%s；已尝试解析，但当前登录账号没有取得可下载播放流。请确认账号已购买、充电或具备对应会员及地区权限后重试。原始错误：%v", restriction, cause)
	}
	_ = updateArchiveItemState(db, item.ID, status, availability, message)
	if item.SubjectID > 0 && item.VersionID > 0 {
		util.SqliteLock.Lock()
		_ = updateVersionAvailability(db, item.SubjectID, item.VersionID, availability, status, message, eventKind)
		util.SqliteLock.Unlock()
	}
	_ = updateInfoState(item.InfoPath, status, availability, message)
	return errors.New(message)
}

func accessRestrictionHint(videoInfo *bilibili.VideoInfo, cause error) string {
	if hint := videoInfo.AccessRestrictionHint(); hint != "" {
		return hint
	}
	message := strings.ToLower(fmt.Sprint(cause))
	for _, keyword := range []string{"充电", "课程", "付费", "购买", "大会员", "会员专享", "电影", "番剧", "试看", "权限", "地区限制", "vip", "pay"} {
		if strings.Contains(message, keyword) {
			return "受限内容"
		}
	}
	var apiErr *bilibili.APIError
	if errors.As(cause, &apiErr) && apiErr.Code == -10403 {
		return "受限内容"
	}
	return ""
}

func updateInfoState(infoPath string, status string, availability string, message string) error {
	if strings.TrimSpace(infoPath) == "" {
		return nil
	}
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return err
	}
	info := map[string]any{}
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	info["archive_state"] = status
	info["availability"] = availability
	info["archive_message"] = message
	info["updated_at"] = time.Now().Format(time.RFC3339)
	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := infoPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, infoPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func prepareArchiveTask(db *sql.DB, item *Item, newTask *task.Task) error {
	if item != nil && item.TaskID > 0 {
		util.SqliteLock.Lock()
		result, err := db.Exec(`UPDATE "task" SET "bvid" = ?, "cid" = ?, "format" = ?, "title" = ?, "owner" = ?, "cover" = ?,
			"status" = 'waiting', "folder" = ?, "duration" = ?, "download_type" = ? WHERE "id" = ?`,
			newTask.Bvid, newTask.Cid, newTask.Format, newTask.Title, newTask.Owner, newTask.Cover,
			newTask.Folder, newTask.Duration, newTask.DownloadType, item.TaskID)
		util.SqliteLock.Unlock()
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			newTask.ID = item.TaskID
			newTask.CreateAt = time.Now()
			return nil
		}
	}
	return newTask.Create(db)
}

func chooseFormat(playInfo *bilibili.PlayInfo, setting string) (common.MediaFormat, error) {
	if playInfo == nil || playInfo.Dash == nil || len(playInfo.Dash.Video) == 0 {
		return 0, fmt.Errorf("播放信息为空")
	}
	var max common.MediaFormat
	for _, media := range playInfo.Dash.Video {
		if media.ID > max {
			max = media.ID
		}
	}
	if setting != "" && setting != "highest" {
		if v, err := strconv.Atoi(setting); err == nil {
			requested := common.MediaFormat(v)
			var fallback common.MediaFormat
			for _, media := range playInfo.Dash.Video {
				if media.ID == requested {
					return requested, nil
				}
				if media.ID < requested && media.ID > fallback {
					fallback = media.ID
				}
			}
			if fallback > 0 {
				return fallback, nil
			}
		}
	}
	return max, nil
}

func getVideoURL(medias []bilibili.Media, format common.MediaFormat, preferredCodec int) (string, error) {
	if len(medias) == 0 {
		return "", fmt.Errorf("视频流为空")
	}
	codecOrder := []int{preferredCodec, 12, 7, 13}
	seen := map[int]bool{}
	for _, code := range codecOrder {
		if seen[code] {
			continue
		}
		seen[code] = true
		for _, item := range medias {
			if item.ID == format && item.Codecid == code && item.BaseURL != "" {
				return item.BaseURL, nil
			}
		}
	}
	return "", fmt.Errorf("未找到对应视频分辨率格式")
}

func getAudioURL(dash *bilibili.Dash, preferHiRes bool) string {
	if dash == nil {
		return ""
	}
	if preferHiRes && dash.Flac != nil && dash.Flac.Audio.BaseURL != "" {
		return dash.Flac.Audio.BaseURL
	}
	var maxAudioID common.MediaFormat
	var audioURL string
	for _, item := range dash.Audio {
		if item.BaseURL != "" && item.ID > maxAudioID {
			maxAudioID = item.ID
			audioURL = item.BaseURL
		}
	}
	return audioURL
}

func effectiveDownloadType(requested string, audioURL string, videoURL string) string {
	switch requested {
	case "audio":
		if audioURL != "" {
			return "audio"
		}
	case "video":
		if videoURL != "" {
			return "video"
		}
	default:
		if audioURL != "" && videoURL != "" {
			return "merge"
		}
		if videoURL != "" {
			return "video"
		}
		if audioURL != "" {
			return "audio"
		}
	}
	return ""
}

func buildTaskTitle(videoInfo *bilibili.VideoInfo, page bilibili.Page, format common.MediaFormat, duration int) string {
	prefix := strings.TrimSpace(videoInfo.Title)
	part := strings.TrimSpace(page.Part)
	if len(videoInfo.Pages) > 1 {
		return fmt.Sprintf("[%s] [%02d] %s [%s] [%dp] [%ds]", prefix, page.Page, part, videoInfo.Owner.Name, format, duration)
	}
	return fmt.Sprintf("%s [%s] [%dp] [%ds]", prefix, videoInfo.Owner.Name, format, duration)
}

func writeSidecars(client *bilibili.BiliClient, folder string, videoInfo *bilibili.VideoInfo, page bilibili.Page, playInfo *bilibili.PlayInfo, settings Settings, filePath string) (string, string, string, error) {
	archiveDir := filepath.Join(folder, "_archive", videoInfo.Bvid, fmt.Sprintf("%03d", page.Page))
	if err := os.MkdirAll(archiveDir, os.ModePerm); err != nil {
		return "", "", "", err
	}
	infoPath := filepath.Join(archiveDir, "info.json")
	coverPath := filepath.Join(archiveDir, "cover.jpg")
	danmakuPath := filepath.Join(archiveDir, "danmaku.xml")
	info := map[string]any{
		"archived_at": time.Now().Format(time.RFC3339),
		"source_url":  "https://www.bilibili.com/video/" + videoInfo.Bvid + "?p=" + strconv.Itoa(page.Page),
		"file_path":   filePath,
		"settings":    settings,
		"video":       videoInfo,
		"page":        page,
		"play_info": map[string]any{
			"accept_quality": playInfo.AcceptQuality,
			"duration":       playInfo.Dash.Duration,
		},
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", "", "", err
	}
	if err := os.WriteFile(infoPath, data, 0644); err != nil {
		return "", "", "", err
	}
	_ = downloadFile(client, videoInfo.Pic, coverPath)
	if dm, err := client.GetDanmaku(page.Cid); err == nil {
		_ = os.WriteFile(danmakuPath, dm, 0644)
	}
	return infoPath, coverPath, danmakuPath, nil
}

func downloadFile(client *bilibili.BiliClient, rawURL string, path string) error {
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return err
	}
	resp, err := client.SimpleGET(rawURL, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func ensureFavoriteShell(db *sql.DB, client *bilibili.BiliClient, settings Settings, fav bilibili.FavoriteItem, subjectID int64, versionID int64, sourceKey string, metadataJSON string) (*Item, bool, error) {
	cid := fav.Ugc.FirstCid
	if cid == 0 && fav.ID > 0 {
		cid = -fav.ID
	}
	if existing, err := getExistingItem(db, fav.Bvid, cid); err != nil {
		return nil, false, err
	} else if existing != nil {
		return existing, false, nil
	}
	folder, err := util.GetArchiveFolder(db)
	if err != nil {
		return nil, false, err
	}
	dirName := strings.TrimPrefix(sourceKey, "bvid:")
	dirName = strings.NewReplacer(":", "-", "/", "-", "\\", "-").Replace(dirName)
	archiveDir := filepath.Join(folder, "_archive", dirName, "000")
	if err := os.MkdirAll(archiveDir, os.ModePerm); err != nil {
		return nil, false, err
	}
	infoPath := filepath.Join(archiveDir, "info.json")
	coverPath := filepath.Join(archiveDir, "cover.jpg")
	snapshot := map[string]any{
		"archived_at":       time.Now().Format(time.RFC3339),
		"archive_state":     "resolving",
		"favorite_media_id": settings.FavMediaID,
		"favorite":          fav,
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(infoPath, data, 0644); err != nil {
		return nil, false, err
	}
	if fav.Cover != "" {
		_ = downloadFile(client, fav.Cover, coverPath)
	}
	util.SqliteLock.Lock()
	result, err := db.Exec(`INSERT INTO "archive_item" (
		"subject_id", "version_id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "duration", "availability", "metadata_json", "status", "message"
	) VALUES (?, ?, 0, ?, ?, 1, ?, ?, ?, '', ?, ?, '', ?, 'unknown', ?, 'resolving', '已从收藏夹发现，正在读取视频信息')`,
		subjectID, versionID, fav.Bvid, cid, fav.Title, fav.Title, fav.Upper.Name, infoPath, coverPath, fav.Duration, metadataJSON)
	if err == nil {
		itemID, _ := result.LastInsertId()
		partResult, partErr := db.Exec(`INSERT INTO "archive_part" ("version_id", "archive_item_id", "cid", "page", "title", "duration", "status", "message") VALUES (?, ?, ?, 1, ?, ?, 'resolving', '已从收藏夹发现')`, versionID, itemID, cid, fav.Title, fav.Duration)
		if partErr == nil {
			partID, _ := partResult.LastInsertId()
			addEvent(db, subjectID, versionID, partID, "queued", "已加入归档时间线，等待解析")
		}
	}
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, false, err
	}
	itemID, _ := result.LastInsertId()
	return &Item{ID: itemID, SubjectID: subjectID, VersionID: versionID, Bvid: fav.Bvid, Cid: cid, Page: 1, Title: fav.Title, Part: fav.Title, Owner: fav.Upper.Name, InfoPath: infoPath, CoverPath: coverPath, Duration: fav.Duration, Availability: "unknown", Status: "resolving"}, true, nil
}

func ensurePagePlaceholder(db *sql.DB, videoInfo *bilibili.VideoInfo, page bilibili.Page, subjectID int64, versionID int64, metadataJSON string) (*Item, error) {
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	var shellID int64
	err := db.QueryRow(`SELECT "id" FROM "archive_item" WHERE "version_id" = ? AND "task_id" = 0 AND "status" IN ('resolving', 'error', 'unavailable') ORDER BY "id" LIMIT 1`, versionID).Scan(&shellID)
	if err == nil {
		_, err = db.Exec(`UPDATE "archive_item" SET "bvid" = ?, "cid" = ?, "page" = ?, "title" = ?, "part" = ?, "owner" = ?,
			"duration" = ?, "availability" = 'available', "metadata_json" = ?, "status" = 'resolving', "message" = '正在解析播放地址', "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`,
			videoInfo.Bvid, page.Cid, page.Page, videoInfo.Title, page.Part, videoInfo.Owner.Name, page.Duration, metadataJSON, shellID)
		if err != nil {
			return nil, err
		}
		_, _ = db.Exec(`UPDATE "archive_part" SET "cid" = ?, "page" = ?, "title" = ?, "duration" = ?, "status" = 'resolving', "message" = '正在解析播放地址', "updated_at" = CURRENT_TIMESTAMP WHERE "archive_item_id" = ?`, page.Cid, page.Page, page.Part, page.Duration, shellID)
		return &Item{ID: shellID, SubjectID: subjectID, VersionID: versionID, Bvid: videoInfo.Bvid, Cid: page.Cid, Page: page.Page, Title: videoInfo.Title, Part: page.Part, Owner: videoInfo.Owner.Name, Duration: page.Duration, Availability: "available", Status: "resolving"}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	result, err := db.Exec(`INSERT INTO "archive_item" (
		"subject_id", "version_id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "duration", "availability", "metadata_json", "status", "message"
	) VALUES (?, ?, 0, ?, ?, ?, ?, ?, ?, '', '', '', '', ?, 'available', ?, 'resolving', '正在解析播放地址')`,
		subjectID, versionID, videoInfo.Bvid, page.Cid, page.Page, videoInfo.Title, page.Part, videoInfo.Owner.Name, page.Duration, metadataJSON)
	if err != nil {
		return nil, err
	}
	itemID, _ := result.LastInsertId()
	partResult, err := db.Exec(`INSERT INTO "archive_part" ("version_id", "archive_item_id", "cid", "page", "title", "duration", "status", "message") VALUES (?, ?, ?, ?, ?, ?, 'resolving', '正在解析播放地址')`, versionID, itemID, page.Cid, page.Page, page.Part, page.Duration)
	if err != nil {
		return nil, err
	}
	partID, _ := partResult.LastInsertId()
	addEvent(db, subjectID, versionID, partID, "queued", fmt.Sprintf("P%d 已加入下载队列", page.Page))
	return &Item{ID: itemID, SubjectID: subjectID, VersionID: versionID, Bvid: videoInfo.Bvid, Cid: page.Cid, Page: page.Page, Title: videoInfo.Title, Part: page.Part, Owner: videoInfo.Owner.Name, Duration: page.Duration, Availability: "available", Status: "resolving"}, nil
}

func updateArchiveItemState(db *sql.DB, itemID int64, status string, availability string, message string) error {
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	_, err := db.Exec(`UPDATE "archive_item" SET "status" = ?, "availability" = ?, "message" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`, status, availability, message, itemID)
	if err == nil {
		_, _ = db.Exec(`UPDATE "archive_part" SET "status" = ?, "message" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "archive_item_id" = ?`, status, message, itemID)
	}
	return err
}

func attachTaskToItem(db *sql.DB, itemID int64, t *task.Task, page bilibili.Page, duration int, filePath string, infoPath string, coverPath string, danmakuPath string) error {
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	_, err := db.Exec(`UPDATE "archive_item" SET "task_id" = ?, "bvid" = ?, "cid" = ?, "page" = ?, "title" = ?, "part" = ?, "owner" = ?,
		"file_path" = ?, "info_path" = ?, "cover_path" = ?, "danmaku_path" = ?, "duration" = ?, "availability" = 'available',
		"status" = 'waiting', "message" = '', "updated_at" = CURRENT_TIMESTAMP WHERE "id" = ?`,
		t.ID, t.Bvid, t.Cid, page.Page, t.Title, page.Part, t.Owner, filePath, infoPath, coverPath, danmakuPath, duration, itemID)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE "archive_part" SET "task_id" = ?, "cid" = ?, "page" = ?, "title" = ?, "duration" = ?, "status" = 'waiting', "message" = '', "updated_at" = CURRENT_TIMESTAMP WHERE "archive_item_id" = ?`, t.ID, t.Cid, page.Page, page.Part, duration, itemID)
	return err
}

func getExistingItem(db *sql.DB, bvid string, cid int) (*Item, error) {
	item := Item{}
	util.SqliteLock.Lock()
	err := db.QueryRow(`SELECT
		"id", "subject_id", "version_id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "duration", "availability", "status", "message",
		"created_at", "updated_at"
		FROM "archive_item" WHERE "bvid" = ? AND "cid" = ?`, bvid, cid).Scan(
		&item.ID, &item.SubjectID, &item.VersionID, &item.TaskID, &item.Bvid, &item.Cid, &item.Page, &item.Title, &item.Part, &item.Owner,
		&item.FilePath, &item.InfoPath, &item.CoverPath, &item.DanmakuPath, &item.Duration, &item.Availability, &item.Status, &item.Message,
		&item.CreatedAt, &item.UpdatedAt,
	)
	util.SqliteLock.Unlock()
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func FindItemIDByTaskID(db *sql.DB, taskID int64) (int64, error) {
	util.SqliteLock.Lock()
	defer util.SqliteLock.Unlock()
	var itemID int64
	err := db.QueryRow(`SELECT "id" FROM "archive_item" WHERE "task_id" = ?`, taskID).Scan(&itemID)
	return itemID, err
}

func getItemsByIDs(db *sql.DB, ids []int64) ([]Item, error) {
	if len(ids) == 0 {
		return []Item{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	util.SqliteLock.Lock()
	rows, err := db.Query(`SELECT
		"id", "subject_id", "version_id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "duration", "availability", "status", "message",
		"created_at", "updated_at"
	FROM "archive_item" WHERE "id" IN (`+strings.Join(placeholders, ",")+`)`, args...)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	archiveFolder, _ := util.GetArchiveFolder(db)
	for rows.Next() {
		item := Item{}
		if err := rows.Scan(
			&item.ID, &item.SubjectID, &item.VersionID, &item.TaskID, &item.Bvid, &item.Cid, &item.Page, &item.Title, &item.Part, &item.Owner,
			&item.FilePath, &item.InfoPath, &item.CoverPath, &item.DanmakuPath, &item.Duration, &item.Availability, &item.Status, &item.Message,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		normalizeItemPaths(&item, archiveFolder)
		items = append(items, item)
	}
	return items, nil
}

func normalizeItemPaths(item *Item, archiveFolder string) {
	item.FilePath = resolveArchiveMediaPath(*item, archiveFolder)
	if archiveFolder != "" {
		pageDir := filepath.Join(archiveFolder, "_archive", item.Bvid, fmt.Sprintf("%03d", item.Page))
		item.InfoPath = resolveSidecarPath(item.InfoPath, filepath.Join(pageDir, "info.json"))
		item.CoverPath = resolveSidecarPath(item.CoverPath, filepath.Join(pageDir, "cover.jpg"))
		item.DanmakuPath = resolveSidecarPath(item.DanmakuPath, filepath.Join(pageDir, "danmaku.xml"))
	}
}

func resolveSidecarPath(oldPath string, currentPath string) string {
	if _, err := os.Stat(oldPath); err == nil {
		return oldPath
	}
	if _, err := os.Stat(currentPath); err == nil {
		return currentPath
	}
	return util.ResolveExistingPath(oldPath)
}

func resolveArchiveMediaPath(item Item, archiveFolder string) string {
	if _, err := os.Stat(item.FilePath); err == nil {
		return item.FilePath
	}
	if archiveFolder == "" || item.TaskID <= 0 {
		return util.ResolveExistingPath(item.FilePath)
	}
	token := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(item.TaskID, 10))), "=", "")
	for _, ext := range []string{".mp4", ".m4a"} {
		matches, err := filepath.Glob(filepath.Join(archiveFolder, "*"+token+ext))
		if err == nil && len(matches) > 0 {
			return matches[0]
		}
	}
	return util.ResolveExistingPath(item.FilePath)
}

func findPageByCID(pages []bilibili.Page, cid int) (bilibili.Page, bool) {
	for _, page := range pages {
		if page.Cid == cid {
			return page, true
		}
	}
	return bilibili.Page{}, false
}

func deleteItemFiles(item Item) error {
	if _, err := deletePreviewFile(item.FilePath); err != nil {
		return err
	}
	for _, path := range []string{item.FilePath, item.InfoPath, item.CoverPath, item.DanmakuPath} {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if item.TaskID > 0 && item.FilePath != "" {
		folder := filepath.Dir(item.FilePath)
		prefix := filepath.Join(folder, strconv.FormatInt(item.TaskID, 10))
		for _, suffix := range []string{".audio", ".audio.part", ".audio.part.etag", ".video", ".video.part", ".video.part.etag"} {
			if err := os.Remove(prefix + suffix); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if item.InfoPath != "" {
		pageDir := filepath.Dir(item.InfoPath)
		removeEmptyDir(pageDir)
		removeEmptyDir(filepath.Dir(pageDir))
	}
	return nil
}

func deletePreviewFile(filePath string) (bool, error) {
	if filePath == "" {
		return false, nil
	}
	path, err := util.GetPreviewPath(util.ResolveExistingPath(filePath))
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func removeEmptyDir(path string) {
	if path == "." || path == string(filepath.Separator) {
		return
	}
	_ = os.Remove(path)
}

func deleteExistingItem(db *sql.DB, item *Item) error {
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
	if _, err := tx.Exec(`DELETE FROM "archive_part" WHERE "archive_item_id" = ?`, item.ID); err != nil && !strings.Contains(err.Error(), "no such table") {
		return rollback(err)
	}
	if item.TaskID > 0 {
		if _, err := tx.Exec(`DELETE FROM "task" WHERE "id" = ?`, item.TaskID); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM "archive_item" WHERE "id" = ?`, item.ID); err != nil {
		return rollback(err)
	}
	if item.VersionID > 0 {
		if _, err := tx.Exec(`DELETE FROM "archive_source_version" WHERE "id" = ? AND NOT EXISTS (SELECT 1 FROM "archive_part" WHERE "version_id" = ?)`, item.VersionID, item.VersionID); err != nil {
			return rollback(err)
		}
	}
	if item.SubjectID > 0 {
		if _, err := tx.Exec(`DELETE FROM "archive_subject" WHERE "id" = ? AND NOT EXISTS (SELECT 1 FROM "archive_source_version" WHERE "subject_id" = ?)`, item.SubjectID, item.SubjectID); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func insertItem(db *sql.DB, t *task.Task, page bilibili.Page, filePath string, infoPath string, coverPath string, danmakuPath string) error {
	util.SqliteLock.Lock()
	_, err := db.Exec(`INSERT INTO "archive_item" (
		"task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "status"
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Bvid, t.Cid, page.Page, t.Title, page.Part, t.Owner,
		filePath, infoPath, coverPath, danmakuPath, "waiting",
	)
	util.SqliteLock.Unlock()
	return err
}

func StartMonitor() {
	if monitorRunning {
		select {
		case monitorWake <- struct{}{}:
		default:
		}
		return
	}
	monitorRunning = true
	go func() {
		for {
			db := util.MustGetDB()
			settings, err := GetSettings(db)
			db.Close()
			if err == nil && settings.Enabled && settings.FavMediaID > 0 {
				db = util.MustGetDB()
				_, _ = ScanFavorite(db)
				db.Close()
			}
			waitMinutes := settings.IntervalMinutes
			if waitMinutes <= 0 {
				waitMinutes = 10
			}
			select {
			case <-time.After(time.Duration(waitMinutes) * time.Minute):
			case <-monitorWake:
			case <-monitorStop:
				monitorRunning = false
				return
			}
		}
	}()
}
