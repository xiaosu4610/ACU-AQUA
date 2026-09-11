package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// 小工具（集中定义，避免 handler 里 import 噪音）

var (
	errEmptyURL       = errors.New("empty image url")
	errDownloadStatus = errors.New("image download bad status")
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func jsonMarshal(v any) []byte            { b, _ := json.Marshal(v); return b }

func contextWithTimeout(p context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(p, d)
}
