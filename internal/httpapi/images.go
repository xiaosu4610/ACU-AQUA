package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
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
	// 密钥有效期 + 分发配额（P4，与 chat 同口径）
	if actx.KeyExpired {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, false, "key_expired", 401)
		errOut(w, 401, "key_expired", "该 API 密钥已过期：请在控制台延长有效期或新建一把密钥")
		return
	}
	keyQ, _ := a.keyQuotaGet(actx.KeyID)
	if code, msg := keyQuotaCheck(keyQ); code != "" {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, false, code, 403)
		errOut(w, 403, code, msg)
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
		// 模型感知选线（与 chat 同一机制，20260919 计费审计修复）：
		// 原用 lineForMode(grp) 取"配置顺序首条同模式线"——per_token 组首条是 codex（GPT 账号池），
		// 图片模型挂在 tide/prime 上时会 404「模型不存在或不支持图片生成」，
		// 用户明明在列表里看得到却调不通。改用 lineForModel 在同模式各线里按模型定位。
		line = a.lineForModel(grp, siteID)
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
	// 软下架（20260923）：图片模型标记维护 → 503（与 chat 同一口径；DB 记录保留，清标记即恢复）
	if model.Maintenance {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, false, "model_maintenance", 503)
		errOut(w, 503, "model_maintenance",
			"模型 "+req.Model+" 维护中，暂不可用（配置与数据已保留，恢复后即可继续调用）；其他模型不受影响")
		return
	}
	// 生效单价（价格组 + 活动价；pricing 键 = 目标线全名）
	grp := billing.UserPriceGrp(a.DB.DB, actx.UserID, "per_call")
	pricing := a.pricingFor(fullID, grp)
	if pricing == nil || pricing.Mode != "per_call" {
		errOut(w, 404, "model_not_found", "该模型已下架或暂不可用")
		return
	}
	// 按张成本保本校验（20260919 计费审计）：售价 < 成本×1/0.97 时每卖一张亏一张。
	// 事前防线（管理台保存时告警）+ 此处运行期兜底（覆盖直接改 DB / 活动价配错）。
	a.warnIfBelowCost(line, pricing, model, pricing.PriceMicro)
	// crowd 线（acu/ 众筹线，20260921 恢复）：请求前过**池子闸门**，不预扣个人余额
	// （池子是共享钱包；扣池在 settleFor 的 crowd 分支完成）
	isCrowd := line.Mode == "crowd"
	if isCrowd {
		if st, code, msg := a.poolGate(actx.UserID); st != 0 {
			a.failRequest0(actx.UserID, actx.KeyHash, req.Model, false, code, st)
			errOut(w, st, code, msg)
			return
		}
		// 防刷：每用户在途并发上限（无并发闸会让单用户无限打上游）
		rel, ok := guard.crowdEnter(actx.UserID)
		if !ok {
			a.failRequest0(actx.UserID, actx.KeyHash, req.Model, false, "crowd_busy", 429)
			errOut(w, 429, "crowd_busy", "当前调用过于频繁（众筹模型每用户同时最多 3 路请求），请等待在途请求完成后再试")
			return
		}
		defer rel()
	}
	rid := a.insertRequestLine(actx.UserID, actx.KeyHash, "images", req.Model, false, line.ID)
	if rid == 0 {
		// 落库失败必须终止：rid=0 的预扣流水永远不会被补偿任务兜底（悬空查询带 request_id>0）
		errOut(w, 500, "internal_error", "请求记录失败")
		return
	}
	// 密钥配额消耗（P4，与 chat 同口径）：结算完成后统一累加（失败/退款不占配额）
	defer a.keyQuotaSettle(rid, actx.KeyID, keyQ)
	prehold := int64(0)
	if !isCrowd {
		prehold = pricing.PriceMicro * req.N
		// 图片模型一律按次计费 → 恒主钱包（2 号折扣钱包只服务按量线的白名单模型）
		if err := billing.Prehold(a.DB.DB, actx.UserID, prehold, rid, billing.WalletMain); err != nil {
			if errors.Is(err, billing.ErrAccountDisabled) {
				a.failRequest(rid, "account_disabled", 403)
				errOut(w, 403, "account_disabled", "账户已被禁用，请联系站长处理")
				return
			}
			a.failRequest(rid, "insufficient_quota", 429) // 诊断 D2：Prehold 失败路径必须回写
			errOut(w, 429, "insufficient_quota", "余额不足：使用收费模型须保持账户 0 元以上余额，请先到控制台充值（先付后用，绝不透支）")
			return
		}
	}

	// 上游
	upBody, err := replaceModel(body, model.UpstreamID)
	if err != nil {
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "internal")
		a.failRequest(rid, "internal_error", 500)
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}
	client, cerr := a.clientFor(line.ID)
	if cerr != nil {
		// 线刚被热重载停用/删除：预扣全额退回（旧实现此处 panic）
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "line_removed")
		a.failRequest(rid, "line_removed", 503)
		errOut(w, 503, "service_unavailable", "线路配置刚刚更新，请稍后重试")
		return
	}
	ctx, cancel := contextWithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	resp, key, err := client.DoKey(ctx, upBody, false, "/images/generations", model.KeyIdx)
	if err != nil {
		if ctxDone(r.Context()) || errors.Is(err, context.Canceled) {
			// 客户端在等待上游响应期间主动断开：与模型健康无关
			a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "client_cancel")
			a.failRequest(rid, "client_cancel", 499)
			return
		}
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
		a.failRequest(rid, "upstream_error", 502)
		errOut(w, 502, "upstream_error", "绘图通道暂时没有响应，请稍后重试")
		return
	}
	defer resp.Body.Close()
	if key != nil {
		a.setRequestKeyIdx(rid, key.RawIdx)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_status")
		a.failRequest(rid, "read_error", 502)
		errOut(w, 502, "upstream_error", "上游服务错误，请稍后重试")
		return
	}
	if resp.StatusCode != 200 {
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_status")
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
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "bad_response")
		a.failRequest(rid, "bad_response", 502)
		errOut(w, 502, "upstream_error", "上游响应异常，请稍后重试")
		return
	}
	// URL 重写 + 防泄露：缓存失败的条目整体剔除（上游原始 URL/B64JSON 绝不残留在响应里），
	// count 只统计实际交付张数（计费按交付计）
	kept := jr.Data[:0]
	for i := range jr.Data {
		d := &jr.Data[i]
		localID, err := a.cacheImage(d.URL, d.B64JSON)
		if err != nil {
			continue
		}
		d.URL = "/v1/images/file/" + localID
		d.B64JSON = ""
		kept = append(kept, *d)
	}
	jr.Data = kept
	count := int64(len(kept))
	final := pricing.PriceMicro * count
	face := int64(0)
	if count > 0 {
		face = model.PerImageCost * count
	}
	a.settleFor(line, actx.UserID, prehold, final, rid, final, "billed_images")
	// 面值台账（按张成本）
	if face > 0 && key != nil {
		client.Pool.ReportFace(key, face)
		_ = billing.LineKeyReport(a.DB.DB, line.ID, key.RawIdx, key.InitialMicro, face, rid)
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
	fline, fm := config.FindFreeModel(a.linesSnap(), model)
	if fm == nil || !fm.Image {
		errOut(w, 404, "model_not_found", "模型不存在或不支持图片生成："+model)
		return
	}
	upBody, err := replaceModel(body, fm.UpstreamID)
	if err != nil {
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}
	client, cerr := a.clientFor(fline.ID)
	if cerr != nil {
		errOut(w, 503, "service_unavailable", "线路配置刚刚更新，请稍后重试")
		return
	}
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
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", err // 非合法 base64：交给调用方剔除该条目，绝不把编码文本当图片落盘
		}
		p := filepath.Join(dir, id+".png")
		if err := os.WriteFile(p, raw, 0o644); err != nil {
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
