package bilibili

import "testing"

func TestVideoInfoAccessRestrictionHint(t *testing.T) {
	tests := []struct {
		name string
		edit func(*VideoInfo)
		want string
	}{
		{
			name: "charge exclusive",
			edit: func(info *VideoInfo) { info.IsUpowerExclusive = true },
			want: "充电专属视频",
		},
		{
			name: "course redirect",
			edit: func(info *VideoInfo) { info.RedirectURL = "https://www.bilibili.com/cheese/play/ep1" },
			want: "课程视频",
		},
		{
			name: "movie",
			edit: func(info *VideoInfo) { info.Rights.Movie = 1 },
			want: "电影或影视付费内容",
		},
		{
			name: "paid video",
			edit: func(info *VideoInfo) { info.Rights.Ugcpay = 1 },
			want: "付费视频",
		},
		{
			name: "ordinary video",
			edit: func(info *VideoInfo) {},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &VideoInfo{}
			tt.edit(info)
			if got := info.AccessRestrictionHint(); got != tt.want {
				t.Fatalf("AccessRestrictionHint() = %q, want %q", got, tt.want)
			}
		})
	}
}
