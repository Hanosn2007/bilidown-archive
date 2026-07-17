package task

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"bilidown/bilibili"
)

func TestDownloadMediaResumesPartialFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=5-" {
			t.Errorf("Range = %q, want bytes=5-", got)
		}
		w.Header().Set("Content-Range", "bytes 5-10/11")
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, " world")
	}))
	defer server.Close()

	folder := t.TempDir()
	partPath := filepath.Join(folder, "7.video.part")
	if err := os.WriteFile(partPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &Task{TaskInDB: TaskInDB{ID: 7, TaskInitOption: TaskInitOption{Folder: folder}}}
	if err := DownloadMedia(context.Background(), &bilibili.BiliClient{}, server.URL, task, "video"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(folder, "7.video"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("download = %q, want hello world", data)
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf("partial file still exists: %v", err)
	}
}
