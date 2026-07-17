package router

import (
	"database/sql"
	"net/http"
	"strconv"

	"bilidown/archive"
	"bilidown/platform"
	"bilidown/task"
	"bilidown/util"
)

// showFile keeps the legacy path-based API available for older clients.
func showFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return
	}
	revealFile(w, r.FormValue("filePath"))
}

func revealTask(w http.ResponseWriter, r *http.Request) {
	id, ok := revealID(w, r)
	if !ok {
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	taskInDB, err := task.GetTask(db, int(id))
	if err == sql.ErrNoRows {
		util.Res{Success: false, Message: "任务不存在"}.Write(w)
		return
	}
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	revealFile(w, taskInDB.FilePath())
}

func archiveReveal(w http.ResponseWriter, r *http.Request) {
	id, ok := revealID(w, r)
	if !ok {
		return
	}
	db := util.MustGetDB()
	defer db.Close()
	item, err := archive.GetItem(db, id)
	if err == sql.ErrNoRows {
		util.Res{Success: false, Message: "归档项不存在"}.Write(w)
		return
	}
	if err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	path := item.InfoPath
	if path == "" {
		path = item.FilePath
	}
	revealFile(w, path)
}

func revealID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Res{Success: false, Message: "参数错误"}.Write(w)
		return 0, false
	}
	return id, true
}

func revealFile(w http.ResponseWriter, path string) {
	if err := platform.Reveal(path); err != nil {
		util.Res{Success: false, Message: err.Error()}.Write(w)
		return
	}
	util.Res{Success: true, Message: "操作成功"}.Write(w)
}
