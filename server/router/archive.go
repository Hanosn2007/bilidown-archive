package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"bilidown/archive"
	"bilidown/util"
)

type archiveBatchReq struct {
	IDs []int64 `json:"ids"`
	All bool    `json:"all"`
}

type archiveLinkVersionReq struct {
	SourceSubjectID int64 `json:"sourceSubjectId"`
	TargetSubjectID int64 `json:"targetSubjectId"`
}

type archiveScanState struct {
	ID       string              `json:"id"`
	Running  bool                `json:"running"`
	Result   *archive.ScanResult `json:"result,omitempty"`
	Error    string              `json:"error,omitempty"`
	Started  string              `json:"startedAt"`
	Finished string              `json:"finishedAt,omitempty"`
}

var archiveScanMux sync.Mutex
var currentArchiveScan archiveScanState

func archiveGetSettings(w http.ResponseWriter, r *http.Request) {
	db := util.MustGetDB()
	defer db.Close()
	settings, err := archive.GetSettings(db)
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	util.Res{Success: true, Data: settings}.Write(w)
}

func archiveSaveSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Res{Success: false, Message: "不支持的请求方法"}.Write(w)
		return
	}
	defer r.Body.Close()
	settings := archive.Settings{}
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	if err := archive.SaveSettings(db, settings); err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	archive.StartMonitor()
	util.Res{Success: true, Message: "保存成功"}.Write(w)
}

func archiveScanFavorite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Res{Success: false, Message: "不支持的请求方法"}.Write(w)
		return
	}
	archiveScanMux.Lock()
	if currentArchiveScan.Running {
		state := currentArchiveScan
		archiveScanMux.Unlock()
		util.Res{Success: true, Message: "扫描已在进行", Data: state}.Write(w)
		return
	}
	currentArchiveScan = archiveScanState{ID: strconv.FormatInt(time.Now().UnixMilli(), 10), Running: true, Started: time.Now().Format(time.RFC3339)}
	state := currentArchiveScan
	archiveScanMux.Unlock()
	go func() {
		db := util.MustGetDB()
		result, err := archive.ScanFavorite(db)
		db.Close()
		archiveScanMux.Lock()
		currentArchiveScan.Running = false
		currentArchiveScan.Result = result
		currentArchiveScan.Finished = time.Now().Format(time.RFC3339)
		if err != nil {
			currentArchiveScan.Error = err.Error()
		}
		archiveScanMux.Unlock()
	}()
	util.Res{Success: true, Message: "扫描已开始", Data: state}.Write(w)
}

func archiveScanStatus(w http.ResponseWriter, r *http.Request) {
	archiveScanMux.Lock()
	state := currentArchiveScan
	archiveScanMux.Unlock()
	util.Res{Success: true, Data: state}.Write(w)
}

func archiveList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	db := util.MustGetDB()
	defer db.Close()
	items, err := archive.ListItems(db, r.URL.Query().Get("q"), limit)
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	util.Res{Success: true, Data: items}.Write(w)
}

func archiveEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	db := util.MustGetDB()
	defer db.Close()
	events, err := archive.ListTimelineEvents(db, after, limit)
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	util.Res{Success: true, Data: events}.Write(w)
}

func archiveSubject(w http.ResponseWriter, r *http.Request) {
	subjectID, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || subjectID <= 0 {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	detail, err := archive.GetSubjectDetail(db, subjectID)
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	util.Res{Success: true, Data: detail}.Write(w)
}

func archiveLinkVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Res{Success: false, Message: "不支持的请求方法"}.Write(w)
		return
	}
	req := archiveLinkVersionReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	if err := archive.LinkSourceSubject(db, req.SourceSubjectID, req.TargetSubjectID); err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	util.Res{Success: true, Message: "来源版本已并入首次归档主体"}.Write(w)
}

func archiveDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Res{Success: false, Message: "不支持的请求方法"}.Write(w)
		return
	}
	req := archiveBatchReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	result, err := archive.DeleteItems(db, req.IDs)
	if err != nil {
		util.Res{Success: false, Message: err.Error(), Data: result}.Write(w)
		return
	}
	util.Res{Success: true, Message: "删除完成", Data: result}.Write(w)
}

func archiveRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Res{Success: false, Message: "不支持的请求方法"}.Write(w)
		return
	}
	req := archiveBatchReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	result, err := archive.RetryItems(db, req.IDs)
	if err != nil {
		util.Res{Success: false, Message: err.Error(), Data: result}.Write(w)
		return
	}
	util.Res{Success: true, Message: "重试完成", Data: result}.Write(w)
}

func archiveDeletePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Res{Success: false, Message: "不支持的请求方法"}.Write(w)
		return
	}
	req := archiveBatchReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	var (
		result *archive.BatchResult
		err    error
	)
	if req.All {
		result, err = archive.ClearPreviewCache()
	} else {
		if len(req.IDs) == 0 {
			util.Res{Success: false, Message: "参数错误"}.Write(w)
			return
		}
		db := util.MustGetDB()
		defer db.Close()
		result, err = archive.DeletePreviewItems(db, req.IDs)
	}
	if err != nil {
		util.Res{Success: false, Message: err.Error(), Data: result}.Write(w)
		return
	}
	util.Res{Success: true, Message: "预览缓存删除完成", Data: result}.Write(w)
}

func archivePreview(w http.ResponseWriter, r *http.Request) {
	source := util.ResolveExistingPath(r.URL.Query().Get("path"))
	if _, err := os.Stat(source); err != nil {
		http.NotFound(w, r)
		return
	}
	download, err := util.GetDefaultDownloadFolder()
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	previewDir := filepath.Join(download, "_preview")
	if err := os.MkdirAll(previewDir, os.ModePerm); err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	previewPath, err := util.GetPreviewPath(source)
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	if _, err := os.Stat(previewPath); err != nil {
		ffmpegPath, err := util.GetFFmpegPath()
		if err != nil {
			util.Res{Success: false, Message: err.Error()}.Write(w)
			return
		}
		tmpPath := previewPath + ".tmp.mp4"
		_ = os.Remove(tmpPath)
		if output, err := createPreview(ffmpegPath, source, tmpPath); err != nil {
			_ = os.Remove(tmpPath)
			util.Res{Success: false, Message: output}.Write(w)
			return
		}
		if err := os.Rename(tmpPath, previewPath); err != nil {
			util.Res{Success: false, Message: err.Error()}.Write(w)
			return
		}
	}
	http.ServeFile(w, r, previewPath)
}

func createPreview(ffmpegPath string, source string, tmpPath string) (string, error) {
	commands := [][]string{softwarePreviewArgs(source, tmpPath)}
	if runtime.GOOS == "darwin" {
		commands = append([][]string{videoToolboxPreviewArgs(source, tmpPath)}, commands...)
	}

	var combinedOutput string
	for _, args := range commands {
		_ = os.Remove(tmpPath)
		cmd := exec.Command(ffmpegPath, args...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			return string(output), nil
		}
		combinedOutput += fmt.Sprintf("$ %s %v\n%s\n", ffmpegPath, args, string(output))
	}
	return combinedOutput, fmt.Errorf("preview transcode failed")
}

func commonPreviewArgs(source string, tmpPath string) []string {
	return []string{
		"-i", source,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "160k",
		"-movflags", "+faststart",
		"-y", tmpPath,
	}
}

func softwarePreviewArgs(source string, tmpPath string) []string {
	args := commonPreviewArgs(source, tmpPath)
	return append(args[:6], append([]string{
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
	}, args[6:]...)...)
}

func videoToolboxPreviewArgs(source string, tmpPath string) []string {
	args := commonPreviewArgs(source, tmpPath)
	return append(args[:6], append([]string{
		"-c:v", "h264_videotoolbox",
		"-b:v", "6000k",
		"-maxrate", "10000k",
		"-tag:v", "avc1",
	}, args[6:]...)...)
}
