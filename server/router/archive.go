package router

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"bilidown/archive"
	"bilidown/util"
)

type archiveBatchReq struct {
	IDs []int64 `json:"ids"`
	All bool    `json:"all"`
}

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
	db := util.MustGetDB()
	defer db.Close()
	result, err := archive.ScanFavorite(db)
	if err != nil {
		util.Res{Success: false, Message: err.Error(), Data: result}.Write(w)
		return
	}
	util.Res{Success: true, Message: "扫描完成", Data: result}.Write(w)
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
		cmd := exec.Command(ffmpegPath,
			"-i", source,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-crf", "23",
			"-pix_fmt", "yuv420p",
			"-c:a", "aac",
			"-b:a", "160k",
			"-movflags", "+faststart",
			"-y", tmpPath,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			_ = os.Remove(tmpPath)
			util.Res{Success: false, Message: string(output)}.Write(w)
			return
		}
		if err := os.Rename(tmpPath, previewPath); err != nil {
			util.Res{Success: false, Message: err.Error()}.Write(w)
			return
		}
	}
	http.ServeFile(w, r, previewPath)
}
