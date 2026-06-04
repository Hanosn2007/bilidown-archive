package archive

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	ID          int64  `json:"id"`
	TaskID      int64  `json:"taskId"`
	Bvid        string `json:"bvid"`
	Cid         int    `json:"cid"`
	Page        int    `json:"page"`
	Title       string `json:"title"`
	Part        string `json:"part"`
	Owner       string `json:"owner"`
	FilePath    string `json:"filePath"`
	InfoPath    string `json:"infoPath"`
	CoverPath   string `json:"coverPath"`
	DanmakuPath string `json:"danmakuPath"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
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

func EnsureTables(db *sql.DB) error {
	util.SqliteLock.Lock()
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
	util.SqliteLock.Unlock()
	return err
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
		"id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "status", "message",
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
			&item.ID, &item.TaskID, &item.Bvid, &item.Cid, &item.Page, &item.Title, &item.Part, &item.Owner,
			&item.FilePath, &item.InfoPath, &item.CoverPath, &item.DanmakuPath, &item.Status, &item.Message,
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
		if item.Status == "waiting" || item.Status == "running" {
			result.Skipped++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 正在下载，已跳过", item.Bvid, item.Cid))
			continue
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
		if item.Status != "error" {
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
		if !ok {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 未找到分P", item.Bvid, item.Cid))
			continue
		}
		_ = deleteItemFiles(item)
		if err := deleteExistingItem(db, &item); err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 清理失败: %v", item.Bvid, item.Cid, err))
			continue
		}
		if err := createPageTask(db, client, settings, videoInfo, page); err != nil {
			result.Failed++
			result.Messages = append(result.Messages, fmt.Sprintf("%s/%d 重新入队失败: %v", item.Bvid, item.Cid, err))
			continue
		}
		result.Retried++
	}
	return result, nil
}

func ScanFavorite(db *sql.DB) (*ScanResult, error) {
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
		created, skipped, failed, messages := createFromBvid(db, client, settings, fav.Bvid)
		result.Created += created
		result.Skipped += skipped
		result.Failed += failed
		result.Messages = append(result.Messages, messages...)
	}
	return result, nil
}

func createFromBvid(db *sql.DB, client *bilibili.BiliClient, settings Settings, bvid string) (created int, skipped int, failed int, messages []string) {
	videoInfo, err := client.GetVideoInfo(bvid)
	if err != nil {
		return 0, 0, 1, []string{fmt.Sprintf("%s 解析失败: %v", bvid, err)}
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
		if existing != nil && existing.Status != "error" {
			skipped++
			continue
		}
		if existing != nil {
			if err := deleteExistingItem(db, existing); err != nil {
				failed++
				messages = append(messages, fmt.Sprintf("%s/%d 清理失败任务失败: %v", videoInfo.Bvid, page.Cid, err))
				continue
			}
		}
		if err := createPageTask(db, client, settings, videoInfo, page); err != nil {
			failed++
			messages = append(messages, fmt.Sprintf("%s/%d 入队失败: %v", videoInfo.Bvid, page.Cid, err))
			continue
		}
		created++
	}
	return
}

func createPageTask(db *sql.DB, client *bilibili.BiliClient, settings Settings, videoInfo *bilibili.VideoInfo, page bilibili.Page) error {
	playInfo, err := client.GetPlayInfo(videoInfo.Bvid, page.Cid)
	if err != nil {
		return err
	}
	if playInfo == nil || playInfo.Dash == nil {
		return fmt.Errorf("播放流信息为空")
	}
	var format common.MediaFormat
	videoURL := ""
	if settings.DownloadType != "audio" {
		if len(playInfo.Dash.Video) > 0 {
			format, err = chooseFormat(playInfo, settings.Format)
			if err != nil {
				return err
			}
			videoURL, err = getVideoURL(playInfo.Dash.Video, format, settings.PreferredCodec)
			if err != nil {
				return err
			}
		}
	}
	audioURL := ""
	if settings.DownloadType != "video" {
		audioURL = getAudioURL(playInfo.Dash, settings.PreferHiResAudio)
	}
	downloadType := effectiveDownloadType(settings.DownloadType, audioURL, videoURL)
	if downloadType == "" {
		return fmt.Errorf("没有可下载的音频或视频流")
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
	if err := newTask.Create(db); err != nil {
		return err
	}
	filePath := newTask.FilePath()
	infoPath, coverPath, danmakuPath, err := writeSidecars(client, folder, videoInfo, page, playInfo, settings, filePath)
	if err != nil {
		return err
	}
	if err := insertItem(db, &newTask, page, filePath, infoPath, coverPath, danmakuPath); err != nil {
		return err
	}
	go newTask.Start()
	return nil
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

func getExistingItem(db *sql.DB, bvid string, cid int) (*Item, error) {
	item := Item{}
	util.SqliteLock.Lock()
	err := db.QueryRow(`SELECT "id", "task_id", "bvid", "cid", "status" FROM "archive_item" WHERE "bvid" = ? AND "cid" = ?`, bvid, cid).
		Scan(&item.ID, &item.TaskID, &item.Bvid, &item.Cid, &item.Status)
	util.SqliteLock.Unlock()
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
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
		"id", "task_id", "bvid", "cid", "page", "title", "part", "owner",
		"file_path", "info_path", "cover_path", "danmaku_path", "status", "message",
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
			&item.ID, &item.TaskID, &item.Bvid, &item.Cid, &item.Page, &item.Title, &item.Part, &item.Owner,
			&item.FilePath, &item.InfoPath, &item.CoverPath, &item.DanmakuPath, &item.Status, &item.Message,
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
	_, err := db.Exec(`DELETE FROM "archive_item" WHERE "id" = ?`, item.ID)
	if err == nil && item.TaskID > 0 {
		_, _ = db.Exec(`DELETE FROM "task" WHERE "id" = ?`, item.TaskID)
	}
	util.SqliteLock.Unlock()
	return err
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
