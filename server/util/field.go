package util

type FieldUtil struct{}

func (f FieldUtil) AllowSelect() []string {
	return []string{
		"download_folder",
		"archive_folder",
		"archive_monitor_enabled",
		"archive_fav_media_id",
		"archive_scan_interval_minutes",
		"archive_download_all_pages",
		"archive_download_type",
		"archive_format",
		"archive_preferred_codec",
		"archive_prefer_hires_audio",
	}
}

func (f FieldUtil) AllowUpdate() []string {
	return []string{
		"download_folder",
		"archive_folder",
		"archive_monitor_enabled",
		"archive_fav_media_id",
		"archive_scan_interval_minutes",
		"archive_download_all_pages",
		"archive_download_type",
		"archive_format",
		"archive_preferred_codec",
		"archive_prefer_hires_audio",
	}
}

func (f FieldUtil) IsAllow(allFields []string, names ...string) bool {
	allowedFields := make(map[string]struct{})
	for _, field := range allFields {
		allowedFields[field] = struct{}{}
	}
	for _, name := range names {
		if _, exists := allowedFields[name]; !exists {
			return false
		}
	}
	return true
}

func (f FieldUtil) IsAllowSelect(names ...string) bool {
	return f.IsAllow(f.AllowSelect(), names...)
}

func (f FieldUtil) IsAllowUpdate(names ...string) bool {
	return f.IsAllow(f.AllowUpdate(), names...)
}
