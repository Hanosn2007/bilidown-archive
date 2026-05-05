package util

import (
	"os"
	"path/filepath"
	"strings"
)

func GetPreviewPath(source string) (string, error) {
	download, err := GetDefaultDownloadFolder()
	if err != nil {
		return "", err
	}
	return filepath.Join(download, "_preview", MD5Hash(source)+".mp4"), nil
}

func ResolveExistingPath(path string) string {
	if path == "" {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	download, err := GetDefaultDownloadFolder()
	if err != nil {
		return path
	}
	normalized := filepath.ToSlash(filepath.Clean(path))
	if idx := strings.Index(normalized, "/_archive/"); idx >= 0 {
		candidate := filepath.Join(download, filepath.FromSlash(normalized[idx+1:]))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(base))
	if ext != ".mp4" && ext != ".m4a" {
		return path
	}
	fields := strings.Fields(base)
	if len(fields) == 0 {
		return path
	}
	token := fields[len(fields)-1]
	matches, err := filepath.Glob(filepath.Join(download, "*"+token))
	if err == nil && len(matches) > 0 {
		return matches[0]
	}
	return path
}
