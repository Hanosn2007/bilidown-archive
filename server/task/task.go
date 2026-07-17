package task

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"bilidown/bilibili"
	"bilidown/common"
	"bilidown/util"
)

// TaskInitOption 创建任务时需要从 POST 请求获取的参数
type TaskInitOption struct {
	Bvid         string             `json:"bvid"`
	Cid          int                `json:"cid"`
	Format       common.MediaFormat `json:"format"`
	Title        string             `json:"title"`
	Owner        string             `json:"owner"`
	Cover        string             `json:"cover"`
	Status       TaskStatus         `json:"status"`
	Folder       string             `json:"folder"`
	Audio        string             `json:"audio"`
	Video        string             `json:"video"`
	Duration     int                `json:"duration"`
	DownloadType string             `json:"downloadType"`
}

// TaskInDB 任务数据库中的数据
type TaskInDB struct {
	TaskInitOption
	ID       int64     `json:"id"`
	CreateAt time.Time `json:"createAt"`
}

func (task *TaskInDB) FilePath() string {
	ext := ".mp4"
	if task.DownloadType == "audio" {
		ext = ".m4a"
	}
	return filepath.Join(task.Folder,
		fmt.Sprintf("%s %s%s", task.Title,
			strings.Replace(base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(task.ID, 10))), "=", "", -1),
			ext,
		),
	)
}

// done | waiting | running | error
type TaskStatus string

type Task struct {
	TaskInDB
	AudioProgress float64 `json:"audioProgress"`
	VideoProgress float64 `json:"videoProgress"`
	MergeProgress float64 `json:"mergeProgress"`
}

var GlobalTaskList = []*Task{}
var GlobalTaskMux = &sync.Mutex{}
var GlobalDownloadSem = util.NewSemaphore(3)
var GlobalMergeSem = util.NewSemaphore(3)

func ActiveTasks() []*Task {
	GlobalTaskMux.Lock()
	defer GlobalTaskMux.Unlock()
	result := make([]*Task, len(GlobalTaskList))
	copy(result, GlobalTaskList)
	return result
}

type taskControl struct {
	cancel context.CancelFunc
	done   chan struct{}
}

var taskControlMux sync.Mutex
var taskControls = map[int64]taskControl{}

func registerTaskControl(id int64) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	control := taskControl{cancel: cancel, done: make(chan struct{})}
	taskControlMux.Lock()
	taskControls[id] = control
	taskControlMux.Unlock()
	return ctx, func() {
		taskControlMux.Lock()
		if current, ok := taskControls[id]; ok && current.done == control.done {
			delete(taskControls, id)
			close(control.done)
		}
		taskControlMux.Unlock()
		GlobalTaskMux.Lock()
		for i, active := range GlobalTaskList {
			if active.ID == id {
				GlobalTaskList = append(GlobalTaskList[:i], GlobalTaskList[i+1:]...)
				break
			}
		}
		GlobalTaskMux.Unlock()
	}
}

// CancelAndWait 取消下载或合并，并等待任务释放文件句柄。
func CancelAndWait(id int64, timeout time.Duration) bool {
	taskControlMux.Lock()
	control, ok := taskControls[id]
	taskControlMux.Unlock()
	if !ok {
		return false
	}
	control.cancel()
	select {
	case <-control.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (task *Task) Create(db *sql.DB) error {
	util.SqliteLock.Lock()
	result, err := db.Exec(`INSERT INTO "task" ("bvid", "cid", "format", "title", "owner", "cover", "status", "folder", "duration", "download_type")
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.Bvid,
		task.Cid,
		task.Format,
		task.Title,
		task.Owner,
		task.Cover,
		task.Status,
		task.Folder,
		task.Duration,
		task.DownloadType,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return err
	}

	task.ID, err = result.LastInsertId()
	task.CreateAt = time.Now()
	return err
}

// Create 创建任务，并将任务加入全局任务列表
func (task *Task) Start() {
	ctx, unregister := registerTaskControl(task.ID)
	defer unregister()
	if task.DownloadType == "" {
		task.DownloadType = "merge"
	}
	GlobalTaskMux.Lock()
	GlobalTaskList = append(GlobalTaskList, task)
	GlobalTaskMux.Unlock()
	db := util.MustGetDB()
	defer db.Close()
	var exists int
	if err := db.QueryRow(`SELECT 1 FROM "task" WHERE "id" = ?`, task.ID).Scan(&exists); err != nil {
		return
	}
	sessdata, err := bilibili.GetSessdata(db)
	if err != nil {
		task.UpdateStatus(db, "error", fmt.Errorf("bilibili.GetSessdata: %v", err))
		return
	}
	client := &bilibili.BiliClient{SESSDATA: sessdata}

	GlobalDownloadSem.Acquire()
	task.UpdateStatus(db, "running")

	if task.DownloadType == "audio" {
		// 仅音频模式：只下载音频，重命名音频文件为输出文件
		err = DownloadMedia(ctx, client, task.Audio, task, "audio")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		GlobalDownloadSem.Release()
		outputPath := task.TaskInDB.FilePath()
		audioPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".audio")
		err = os.Rename(audioPath, outputPath)
		if err != nil {
			task.UpdateStatus(db, "error", fmt.Errorf("os.Rename: %v", err))
			return
		}
		// 添加元数据
		if err := task.addMetadata(outputPath); err != nil {
			log.Printf("添加元数据失败 (任务ID: %d): %v", task.ID, err)
		}
		task.UpdateStatus(db, "done")
		return
	} else if task.DownloadType == "video" {
		// 仅视频模式：只下载视频，重命名视频文件为输出文件
		err = DownloadMedia(ctx, client, task.Video, task, "video")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		GlobalDownloadSem.Release()
		outputPath := task.TaskInDB.FilePath()
		videoPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".video")
		err = os.Rename(videoPath, outputPath)
		if err != nil {
			task.UpdateStatus(db, "error", fmt.Errorf("os.Rename: %v", err))
			return
		}
		// 添加元数据
		if err := task.addMetadata(outputPath); err != nil {
			log.Printf("添加元数据失败 (任务ID: %d): %v", task.ID, err)
		}
		task.UpdateStatus(db, "done")
		return
	} else {
		// 合并模式：下载音频和视频，然后合并
		err = DownloadMedia(ctx, client, task.Audio, task, "audio")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		err = DownloadMedia(ctx, client, task.Video, task, "video")
		if err != nil {
			GlobalDownloadSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("DownloadMedia: %v", err))
			return
		}
		GlobalDownloadSem.Release()

		outputPath := task.TaskInDB.FilePath()
		videoPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".video")
		audioPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+".audio")
		GlobalMergeSem.Acquire()
		err = task.MergeMedia(ctx, outputPath, videoPath, audioPath)
		if err != nil {
			GlobalMergeSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("task.MergeMedia: %v", err))
			return
		}
		err = os.Remove(videoPath)
		if err != nil {
			GlobalMergeSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("os.Remove: %v", err))
			return
		}
		err = os.Remove(audioPath)
		if err != nil {
			GlobalMergeSem.Release()
			task.UpdateStatus(db, "error", fmt.Errorf("os.Remove: %v", err))
			return
		}
		GlobalMergeSem.Release()
		// 添加元数据
		if err := task.addMetadata(outputPath); err != nil {
			log.Printf("添加元数据失败 (任务ID: %d): %v", task.ID, err)
		}
		task.UpdateStatus(db, "done")
	}
}

// 合并音视频
func (task *Task) MergeMedia(ctx context.Context, outputPath string, inputPaths ...string) error {
	inputs := []string{}
	for _, path := range inputPaths {
		inputs = append(inputs, "-i", path)
	}

	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, append(inputs, "-c:v", "copy", "-c:a", "copy", "-progress", "pipe:1", "-strict", "-2", outputPath)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)

	progress := newProgressBar(int64(task.Duration))
	outTimeRegex := regexp.MustCompile(`out_time_ms=(\d+)`) // 毫秒

	for scanner.Scan() {
		line := scanner.Text()
		match := outTimeRegex.FindStringSubmatch(line)
		if len(match) == 2 {
			outTime, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil {
				return err
			}
			progress.current = outTime / 1000000
			task.MergeProgress = progress.percent()
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}
	task.MergeProgress = 1
	return nil
}

func GetVideoURL(medias []bilibili.Media, format common.MediaFormat) (string, error) {
	for _, code := range []int{12, 7, 13} {
		for _, item := range medias {
			if item.ID == format && item.Codecid == code {
				return item.BaseURL, nil
			}
		}
	}
	return "", errors.New("未找到对应视频分辨率格式")
}

func GetAudioURL(dash *bilibili.Dash) string {
	if dash.Flac != nil {
		return dash.Flac.Audio.BaseURL
	}
	var maxAudioID common.MediaFormat
	var audioURL string
	for _, item := range dash.Audio {
		if item.ID > maxAudioID {
			maxAudioID = item.ID
			audioURL = item.BaseURL
		}
	}
	return audioURL
}

func (task *Task) UpdateStatus(db *sql.DB, status TaskStatus, errs ...error) error {
	util.SqliteLock.Lock()
	_, err := db.Exec(`UPDATE "task" SET "status" = ? WHERE "id" = ?`, status, task.ID)
	_, archiveErr := db.Exec(`UPDATE "archive_item" SET "status" = ?, "message" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "task_id" = ?`,
		status, joinErrors(errs...), task.ID)
	_, partErr := db.Exec(`UPDATE "archive_part" SET "status" = ?, "message" = ?, "updated_at" = CURRENT_TIMESTAMP WHERE "task_id" = ?`,
		status, joinErrors(errs...), task.ID)
	_, subjectErr := db.Exec(`UPDATE "archive_subject" SET "status" = CASE
		WHEN EXISTS (SELECT 1 FROM "archive_item" ai WHERE ai."subject_id" = "archive_subject"."id" AND ai."status" IN ('resolving', 'waiting', 'running')) THEN 'downloading'
		WHEN EXISTS (SELECT 1 FROM "archive_item" ai WHERE ai."subject_id" = "archive_subject"."id" AND ai."status" = 'error') THEN 'error'
		WHEN EXISTS (SELECT 1 FROM "archive_item" ai WHERE ai."subject_id" = "archive_subject"."id" AND ai."status" = 'done') THEN 'done'
		ELSE 'unavailable' END,
		"updated_at" = CURRENT_TIMESTAMP
		WHERE "id" IN (SELECT "subject_id" FROM "archive_item" WHERE "task_id" = ?)`, task.ID)
	util.SqliteLock.Unlock()
	if err != nil {
		return err
	}
	if archiveErr != nil {
		return archiveErr
	}
	if partErr != nil && !strings.Contains(partErr.Error(), "no such table") {
		return partErr
	}
	if subjectErr != nil && !strings.Contains(subjectErr.Error(), "no such table") {
		return subjectErr
	}
	for _, err := range errs {
		if err != nil {
			err = util.CreateLog(db, fmt.Sprintf("Task-%d-Error: %v", task.ID, err))
			if err != nil {
				log.Fatalln("CreateLog:", err)
			}
		}
	}
	task.Status = status
	return err
}

func joinErrors(errs ...error) string {
	messages := []string{}
	for _, err := range errs {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	return strings.Join(messages, "; ")
}

func DownloadMedia(ctx context.Context, client *bilibili.BiliClient, mediaURL string, task *Task, mediaType string) error {
	if mediaURL == "" {
		return fmt.Errorf("%s media url is empty", mediaType)
	}
	finalPath := filepath.Join(task.Folder, strconv.FormatInt(task.ID, 10)+"."+mediaType)
	if info, err := os.Stat(finalPath); err == nil && info.Size() > 0 {
		return nil
	}
	partPath := finalPath + ".part"
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := downloadMediaAttempt(ctx, client, mediaURL, task, mediaType, partPath); err == nil {
			if err := os.Rename(partPath, finalPath); err != nil {
				return err
			}
			_ = os.Remove(partPath + ".etag")
			return nil
		} else {
			lastErr = err
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 350 * time.Millisecond):
			}
		}
	}
	return lastErr
}

func downloadMediaAttempt(ctx context.Context, client *bilibili.BiliClient, mediaURL string, task *Task, mediaType string, partPath string) error {
	var offset int64
	if info, err := os.Stat(partPath); err == nil {
		offset = info.Size()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return err
	}
	request.Header = client.MakeHeader()
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if etag, err := os.ReadFile(partPath + ".etag"); err == nil && len(etag) > 0 {
			request.Header.Set("If-Range", string(etag))
		}
	}
	httpClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(nil)}}
	resp, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			_ = os.Remove(partPath)
			_ = os.Remove(partPath + ".etag")
		}
		return fmt.Errorf("download status %d", resp.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 && resp.StatusCode == http.StatusPartialContent && strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
		flags |= os.O_APPEND
	} else {
		offset = 0
		flags |= os.O_TRUNC
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		_ = os.WriteFile(partPath+".etag", []byte(etag), 0644)
	}
	file, err := os.OpenFile(partPath, flags, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	total := resp.ContentLength
	if total > 0 {
		total += offset
	}
	progress := newProgressBar(total)
	progress.current = offset
	buf := make([]byte, 128*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				return err
			}
			progress.add(n)
			GlobalTaskMux.Lock()
			if mediaType == "video" {
				task.VideoProgress = progress.percent()
			} else {
				task.AudioProgress = progress.percent()
			}
			GlobalTaskMux.Unlock()
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	return file.Sync()
}

type progressBar struct {
	total   int64
	current int64
}

func (p *progressBar) add(n int) {
	p.current += int64(n)
}

func (p *progressBar) percent() float64 {
	if p.total <= 0 {
		return 0
	}
	return float64(p.current) / float64(p.total)
}

func newProgressBar(total int64) *progressBar {
	return &progressBar{
		total: total,
	}
}

func GetTaskList(db *sql.DB, page int, pageSize int) ([]TaskInDB, error) {
	tasks := []TaskInDB{}
	util.SqliteLock.Lock()
	rows, err := db.Query(`SELECT
		"id", "bvid", "cid", "format", "title",
		"owner", "cover", "status", "folder", "duration", "download_type", "create_at"
	FROM "task" ORDER BY "id" DESC LIMIT ?, ?`,
		page*pageSize, pageSize,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}

	createAt := ""

	for rows.Next() {
		task := TaskInDB{}
		err = rows.Scan(
			&task.ID,
			&task.Bvid,
			&task.Cid,
			&task.Format,
			&task.Title,
			&task.Owner,
			&task.Cover,
			&task.Status,
			&task.Folder,
			&task.Duration,
			&task.DownloadType,
			&createAt,
		)
		if err != nil {
			return nil, err
		}
		task.CreateAt, err = time.Parse("2006-01-02 15:04:05", createAt)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func DeleteTask(db *sql.DB, taskID int) error {
	util.SqliteLock.Lock()
	_, err := db.Exec(`DELETE FROM "task" WHERE "id" = ?`, taskID)
	util.SqliteLock.Unlock()
	return err
}

func GetTask(db *sql.DB, taskID int) (*TaskInDB, error) {
	task := TaskInDB{}
	createAt := ""
	util.SqliteLock.Lock()
	err := db.QueryRow(`SELECT
		"id", "bvid", "cid", "format", "title",
		"owner", "cover", "status", "folder", "duration", "download_type", "create_at"
	FROM "task" WHERE "id" = ?`,
		taskID,
	).Scan(
		&task.ID,
		&task.Bvid,
		&task.Cid,
		&task.Format,
		&task.Title,
		&task.Owner,
		&task.Cover,
		&task.Status,
		&task.Folder,
		&task.Duration,
		&task.DownloadType,
		&createAt,
	)
	util.SqliteLock.Unlock()
	if err != nil {
		return nil, err
	}

	task.CreateAt, err = time.Parse("2006-01-02 15:04:05", createAt)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// addMetadata 使用 ffmpeg 给输出文件添加基础媒体元数据，完整归档信息另存为 sidecar JSON。
func (task *Task) addMetadata(filePath string) error {
	ffmpegPath, err := util.GetFFmpegPath()
	if err != nil {
		return err
	}

	desc := fmt.Sprintf("BVID: %s; CID: %d", task.Bvid, task.Cid)
	if desc == "" {
		desc = ""
	}

	author := task.Owner

	// 临时文件加上 .mp4 扩展名
	tempPath := filePath + ".tmp.mp4"

	// 使用双引号包裹文件路径，避免特殊字符
	cmd := exec.Command(ffmpegPath,
		"-i", filePath,
		"-metadata", "title="+task.Title,
		"-metadata", "description="+desc,
		"-metadata", "comment=https://www.bilibili.com/video/"+task.Bvid,
		"-metadata", "artist="+author,
		"-codec", "copy",
		"-y",
		tempPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg添加元数据失败: %v, 输出: %s", err, string(output))
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("删除原文件失败: %v", err)
	}
	if err := os.Rename(tempPath, filePath); err != nil {
		return fmt.Errorf("重命名临时文件失败: %v", err)
	}

	return nil
}
