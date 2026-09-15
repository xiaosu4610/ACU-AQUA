package httpapi

// 测试构建专用：e2e 测试同 IP 高频请求，关闭 per-IP 限流避免误伤
func init() { guardBypass = true }
