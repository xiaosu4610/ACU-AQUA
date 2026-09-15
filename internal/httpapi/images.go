package httpapi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/util"
)

// imageStore 已生成图片的站内中转登记（内存 + data/images 落盘，重启后仍可下载）
var (
	imageMu    sync.Mutex
	imageFiles = map[string]string{} // id → 本地文件路径
)

// handleImages 图片生成（按张计费：n×单价预扣 → 上游 → URL 重写站内中转 → 结算）
func (a *App) handleImages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// 通道防刷：per-IP 滑窗限流（与 chat 同一口径，挡在打上游之前）
	if !guard.allow(clientIP(r)) {
		guardReject(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败")
		return
	}
	var req struct {
		Model string `json:"model"`
		N     int64  `json:"n"`
	}
	if err := jsonUnmarshal(body, &req); err != nil || req.Model == "" {
		errOut(w, 400, "bad_request", "请求体格式错误或缺 model 字段")
		return
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 10 {
		errOut(w, 400, "bad_request", "单次最多生成 10 张")
		return
	}
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "invalid_api_key", "请先登录或提供有效的 API 密钥")
		return
	}
	lineID, siteID, hasPrefix := config.SplitModel(req.Model)
	unified := hasPrefix && a.Cfg.Billing.UnifiedPrefix != "" && lineID == a.Cfg.Billing.UnifiedPrefix
	var line *config.Line
	if hasPrefix && !unified {
		line = a.lineByID(lineID)
	}
	if !hasPrefix || (line == nil && !unified) || (line != nil && line.Mode == "free") {
		// 免费图像模型（裸名如 cogview-3-flash）：转发 + URL 站内重写，不计费
		a.handleFreeImages(w, r, body, &req, actx.UserID, actx.KeyHash)
		return
	}
	if unified {
		// 统一前缀：按密钥计费分组选线（与 chat 同一机制）
		grp := actx.KeyGrp
		if grp == "" {
			grp = a.Cfg.Billing.DefaultGrp
		}
		if grp == "free" {
			errOut(w, 403, "free_grp_restricted", "当前密钥为纯免费分组，仅可调用免费模型；收费模型请到控制台切换密钥分组")
			return
		}
		line = a.lineForMode(grp)
		if line == nil {
			if grp == "per_call" {
				// 按次线临时下架（admin_lines.enabled=0）：明确告知并引导切换按量分组
				errOut(w, 403, "per_call_suspended", "按次计费已临时下架，当前仅按量计费开放——请在控制台将该密钥的计费分组切换为「按量计费」后重试")
				return
			}
			errOut(w, 503, "service_unavailable", "未配置 "+grp+" 计费线，请联系站长")
			return
		}
	}
	var model *config.Model
	for i := range line.Models {
		if line.Models[i].SiteID == siteID && line.Models[i].Image {
			model = &line.Models[i]
			break
		}
	}
	if model == nil {
		if unified {
			errOut(w, 404, "model_not_found", "模型不存在或不支持图片生成："+req.Model+"（该模型不在当前密钥计费分组的可用列表）")
			return
		}
		errOut(w, 404, "model_not_found", "模型不存在或不支持图片生成："+req.Model)
		return
	}
	fullID := config.ModelFullName(line.ID, siteID)
	// 生效单价（价格组 + 活动价；pricing 键 = 目标线全名）
	grp := billing.UserPriceGrp(a.DB.DB, actx.UserID, "per_call")
	pricing := a.pricingFor(fullID, grp)
	if pricing == nil || pricing.Mode != "per_call" {
		errOut(w, 404, "model_not_found", "该模型已下架或暂不可用")
		return
	}
	rid := a.insertRequestLine(actx.UserID, actx.KeyHash, "images", req.Model, false, line.ID)
	prehold := pricing.PriceMicro * req.N
	if err := billing.Prehold(a.DB.DB, actx.UserID, prehold, rid); err != nil {
		a.failRequest(rid, "insufficient_quota", 429) // 诊断 D2：Prehold 失败路径必须回写
		errOut(w, 429, "insufficient_quota", "余额不足：使用收费模型须保持账户 0 元以上余额，请先到控制台充值（先付后用，绝不透支）")
		return
	}

	// 上游
	upBody, err := replaceModel(body, model.UpstreamID)
	if err != nil {
		a.settleSafely(actx.UserID, prehold, 0, rid, 0, "internal")
		a.failRequest(rid, "internal_error", 500)
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}
	client := a.clientFor(line.ID)
	ctx, cancel := contextWithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	resp, key, err := client.DoKey(ctx, upBody, false, "/images/generations", model.KeyIdx)
	if err != nil {
		a.settleSafely(actx.UserID, prehold, 0, rid, 0, "upstream_error")
		a.failRequest(rid, "upstream_error", 502)
		errOut(w, 502, "upstream_error", "线路繁忙：已自动换线重试仍失败，请稍后重试")
		return
	}
	defer resp.Body.Close()
	if key != nil {
		a.setRequestKeyIdx(rid, key.Idx)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		a.settleSafely(actx.UserID, prehold, 0, rid, 0, "upstream_status")
		a.failRequest(rid, "read_error", 502)
		errOut(w, 502, "upstream_error", "上游服务错误，请稍后重试")
		return
	}
	if resp.StatusCode != 200 {
		a.settleSafely(actx.UserID, prehold, 0, rid, 0, "upstream_status")
		a.failRequest(rid, "upstream_status", resp.StatusCode)
		// 信息隔离：上游报错转译为站点标准错误码，不透传原文
		upstreamErrOut(w, resp.StatusCode, raw)
		return
	}

	// URL 重写：上游 OSS URL → 站内 /v1/images/file/{id}（防上游地址泄露）
	var jr struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := jsonUnmarshal(raw, &jr); err != nil || len(jr.Data) == 0 {
		a.settleSafely(actx.UserID, prehold, 0, rid, 0, "bad_response")
		a.failRequest(rid, "bad_response", 502)
		errOut(w, 502, "upstream_error", "上游响应异常，请稍后重试")
		return
	}
	// 实际张数计费（n=3 但上游只回 2 张 → 只收 2 张）
	count := int64(0)
	for i := range jr.Data {
		d := &jr.Data[i]
		localID, err := a.cacheImage(d.URL, d.B64JSON)
		if err != nil {
			continue
		}
		d.URL = "/v1/images/file/" + localID
		d.B64JSON = ""
		count++
	}
	final := pricing.PriceMicro * count
	face := int64(0)
	if count > 0 {
		face = model.PerImageCost * count
	}
	a.settleSafely(actx.UserID, prehold, final, rid, final, "billed_images")
	// 面值台账（按张成本）
	if face > 0 && key != nil {
		client.Pool.ReportFace(key, face)
		_ = billing.LineKeyReport(a.DB.DB, line.ID, key.Idx, key.InitialMicro, face, rid)
	}
	// 按张计费不看 token，usage_source 记 estimated（token 口径无上游 usage）
	a.okRequest(rid, billing.Usage{}, final, face, "estimated", time.Since(start).Milliseconds(), 200)

	out := stripSensitive(jsonMarshal(jr))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
}

// handleFreeImages 免费图像模型：转发 → URL 站内重写 → usage 记账（不计费）
func (a *App) handleFreeImages(w http.ResponseWriter, r *http.Request, body []byte, req *struct {
	Model string `json:"model"`
	N     int64  `json:"n"`
}, uid int64, keyHash string) {
	start := time.Now()
	model := config.NormalizeModel(req.Model)
	fline, fm := a.Cfg.FindFreeModel(model)
	if fm == nil || !fm.Image {
		errOut(w, 404, "model_not_found", "模型不存在或不支持图片生成：" + model)
		return
	}
	upBody, err := replaceModel(body, fm.UpstreamID)
	if err != nil {
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}
	client := a.clientFor(fline.ID)
	ctx, cancel := contextWithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	resp, _, err := client.Do(ctx, upBody, false, "/images/generations")
	if err != nil {
		errOut(w, 502, "upstream_error", "免费通道暂时不可用，请稍后重试")
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		errOut(w, 502, "upstream_error", "上游响应读取失败")
		return
	}
	if resp.StatusCode != 200 {
		upstreamErrOut(w, resp.StatusCode, raw)
		return
	}
	rid := a.insertRequest(uid, keyHash, "images", model, false)
	var jr struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := jsonUnmarshal(raw, &jr); err != nil || len(jr.Data) == 0 {
		a.failRequest(rid, "bad_response", 502)
		errOut(w, 502, "upstream_error", "上游响应异常，请稍后重试")
		return
	}
	// URL 重写：上游 OSS URL → 站内 /v1/images/file/{id}（防上游地址泄露）
	for i := range jr.Data {
		d := &jr.Data[i]
		localID, err := a.cacheImage(d.URL, d.B64JSON)
		if err != nil {
			continue
		}
		d.URL = "/v1/images/file/" + localID
		d.B64JSON = ""
	}
	a.okFreeRequest(rid, billing.Usage{}, 200, "estimated", time.Since(start).Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(stripSensitive(jsonMarshal(jr)))
}

// cacheImage 把上游图片缓存到本地 data/images/（成功返回站内 id）
func (a *App) cacheImage(url, b64 string) (string, error) {
	id := util.RandHex(12)
	dir := filepath.Join("data", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if b64 != "" {
		p := filepath.Join(dir, id+".png")
		if err := os.WriteFile(p, []byte(b64), 0o644); err != nil {
			return "", err
		}
		imageMu.Lock()
		imageFiles[id] = p
		imageMu.Unlock()
		return id, nil
	}
	if url == "" {
		return "", errEmptyURL
	}
	// 下载上游图片
	c := &http.Client{Timeout: 120 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errDownloadStatus
	}
	ext := ".png"
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "jpeg") {
		ext = ".jpg"
	} else if strings.Contains(ct, "webp") {
		ext = ".webp"
	}
	p := filepath.Join(dir, id+ext)
	f, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(p)
		return "", err
	}
	imageMu.Lock()
	imageFiles[id] = p
	imageMu.Unlock()
	return id, nil
}

// handleImageFile 站内图片中转下载（免鉴权——图片 id 是 24 位随机 hex 不可枚举）
func (a *App) handleImageFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) > 32 || !util.IsHex(id) {
		errOut(w, 400, "bad_request", "参数错误")
		return
	}
	imageMu.Lock()
	p, ok := imageFiles[id]
	imageMu.Unlock()
	if !ok {
		// 重启恢复：从磁盘找
		matches, _ := filepath.Glob(filepath.Join("data", "images", id+".*"))
		if len(matches) == 0 {
			errOut(w, 404, "not_found", "图片不存在或已过期")
			return
		}
		p = matches[0]
		imageMu.Lock()
		imageFiles[id] = p
		imageMu.Unlock()
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	default:
		w.Header().Set("Content-Type", "image/png")
	}
	http.ServeFile(w, r, p)
}
