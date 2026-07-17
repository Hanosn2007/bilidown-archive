package archive

import (
	"errors"
	"testing"

	"bilidown/bilibili"
)

func TestAccessRestrictionHint(t *testing.T) {
	chargeInfo := &bilibili.VideoInfo{IsUpowerExclusive: true}
	if got := accessRestrictionHint(chargeInfo, errors.New("播放流信息为空")); got != "充电专属视频" {
		t.Fatalf("charge hint = %q", got)
	}

	apiErr := &bilibili.APIError{Code: -10403, Message: "forbidden"}
	if got := accessRestrictionHint(&bilibili.VideoInfo{}, apiErr); got != "受限内容" {
		t.Fatalf("API restriction hint = %q", got)
	}

	if got := accessRestrictionHint(&bilibili.VideoInfo{}, errors.New("temporary network error")); got != "" {
		t.Fatalf("ordinary error hint = %q", got)
	}
}
