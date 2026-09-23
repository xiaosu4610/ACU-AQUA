package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/upstream"
)

// readerBody 把 bufio.Reader 包装成 ReadCloser（首帧探测后缓冲数据自然衔接后续读取）
type readerBody struct {
	io.Reader
	io.Closer
}

// chatReq chat/completions 请求体（透传字段用 raw 保真）
type chatReq struct {
	Model     string      `json:"model"`
	Stream    bool        `json:"stream"`
	MaxTokens json.Number `json:"max_tokens,omitempty"`
	// Messages 请求消息原文（仅用于无上游 usage 时的输入侧保守估算，20260919 亏本防线）
	Messages      json.RawMessage `json:"messages,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Raw json.RawMessage `json:"-"`
}

// usage JSON（上游响应内）
type usageJSON struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	// 缓存命中兼容字段：部分上游不用 OpenAI 标准结构（缺失时缓存 token 按全价输入
	// 计费——用户被超收）。DeepSeek 旧口径 / Anthropic 网关透传口径
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
}

// handleChat /v1/chat/completions 主入口：
// 鉴权 → 收费线路由（统一前缀按密钥分组选线 / 线前缀显式直连）→ 免费模型（含 auto 路由/动态目录/旧 ID 兼容）
//
// legacyPaidRetired 已下架收费模型的裸名指引（路由隔离，20260919）：
// 裸名命中已下架收费模型时不再掉进免费线同名模型（付费意图用户会被商汤免费线的
// 429/断流误导为"收费渠道故障"），直接 410 给出明确替代指引。
var legacyPaidRetired = map[string]string{
	"deepseek-v4-pro": "aqua/deepseek-v4-1-flash",
}

// → 预扣→上游→结算（多退少补）
func (a *App) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// 通道防刷：per-IP 滑窗限流（挡在打上游之前，保 kabuai 上游不被刷满殃及正常计费线）
	if !guard.allow(clientIP(r)) {
		guardReject(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败")
		return
	}
	var req chatReq
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		errOut(w, 400, "bad_request", "请求体格式错误或缺 model 字段")
		return
	}
	// 收费路由判定：统一前缀（按密钥分组）/ 线前缀（显式直连）；其余走免费分发
	// （无前缀裸模型 / 旧厂商前缀 zhipu/glm-4-flash / auto 智能路由 / 动态目录）
	lineID, siteID, hasPrefix := config.SplitModel(req.Model)
	unified := hasPrefix && a.Cfg.Billing.UnifiedPrefix != "" && lineID == a.Cfg.Billing.UnifiedPrefix
	// 裸名兼容路由（20260918 引入，20260922 修正）：旧众筹时代客户端以裸名调用收费模型
	// （deepseek-v4-pro 等），此前被免费线同名模型截胡，用户误以为"收费渠道故障"。
	//
	// ⚠️ 20260922 事故：原判断只查了「裸名是不是收费线的 SiteID」，**漏了"非免费线"这半边**，
	// 而 glm-5.3 / glm-5.3-flash / kimi-k3 / deepseek-v4.1-flash 等**同时在免费目录与收费线**
	// （都是收费线的 SiteID）→ 用户调裸名想用免费公益通道，却被路由到 aqua/prime **扣了钱**。
	// 生产实锤：22 个用户、1432 次请求被误扣 ¥18.58。
	// 修正口径（即原注释本意）：**裸名只要由免费线提供（配置目录或动态目录），一律走免费**——
	// /v1/models 里它就是以免费模型示人的，绝不能静默扣费；只有免费线没有的裸名才视同统一前缀计费。
	if !hasPrefix && a.Cfg.Billing.UnifiedPrefix != "" &&
		a.paidLineForSiteID(req.Model) != nil && !a.freeLineHasModel(req.Model) {
		unified = true
		siteID = config.NormalizeModel(req.Model)
	}
	// 裸名命中已下架收费模型：410 明确指引，不落免费线（20260919 路由隔离）
	if !unified && !hasPrefix {
		if alt, ok := legacyPaidRetired[config.NormalizeModel(req.Model)]; ok {
			// 410 拒绝落 requests（此前盲区）：此点尚未鉴权，按匿名口径记录
			a.failRequest0(0, "", req.Model, req.Stream, "model_retired", 410)
			errOut(w, 410, "model_retired",
				"模型 "+req.Model+" 已正式下架：旗舰由 "+alt+" 全面代偿（按次计费）；免费体验请使用 acu/"+config.NormalizeModel(req.Model))
			return
		}
	}
	var line *config.Line
	if hasPrefix && !unified {
		line = a.lineByID(lineID)
	}
	if !unified && (line == nil || line.Mode == "free") {
		// 绞杀者模式：免费线仍由旧网关承接（免 Go 鉴权，旧网关自管）
		if legacyProxy != nil {
			a.proxyChat(w, r, body)
			return
		}
		// 纯 Go 模式：免费线 open 模式（与 Rust AUTH_MODE=open 等价）——
		// 免登录直接对话（体验中心/树洞/竞技场依赖）；带凭据则 usage 记到用户
		actx := auth.Authenticate(a.DB.DB, r)
		uid, kh := int64(0), ""
		if actx != nil {
			uid, kh = actx.UserID, actx.KeyHash
		}
		a.handleFreeChat(w, r, body, &req, uid, kh)
		return
	}
	// 鉴权（收费线强制登录/API 密钥；统一前缀分组路由需要密钥分组）
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "invalid_api_key", "请先登录或提供有效的 API 密钥")
		return
	}
	// 密钥有效期（P4）：过期密钥单独错误码，便于下游区分"过期"与"密钥错"
	if actx.KeyExpired {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "key_expired", 401)
		errOut(w, 401, "key_expired", "该 API 密钥已过期：请在控制台延长有效期或新建一把密钥")
		return
	}
	// 密钥分发配额（P4）：超限拦截（虚拟额度，不涉及资金扣减；实扣仍在账户余额）
	keyQ, _ := a.keyQuotaGet(actx.KeyID)
	if code, msg := keyQuotaCheck(keyQ); code != "" {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, code, 403)
		errOut(w, 403, code, msg)
		return
	}
	// 秒败风暴断路器：该密钥连续快速上游失败已触发冷却 → 429 拦截（保护上游配额，逼迫客户端退避）
	if !stormCheck(actx.KeyHash) {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "storm_cooldown", 429)
		errOut(w, 429, "rate_limited", "请求频率过高：你的客户端在连续快速失败后仍在重试，已临时限流 60 秒——请为程序增加失败退避（如指数重试）后再试")
		return
	}
	if line != nil && line.Mode == "official" {
		// tlk 官方中转线（线前缀直连）：仅官方中转分组密钥可调（独占高速模型，官方原价 6 折计费）
		kg := actx.KeyGrp
		if kg == "" {
			kg = a.Cfg.Billing.DefaultGrp
		}
		if kg != "official" {
			errOut(w, 403, "official_line_restricted", "tlk/ 官方中转模型仅限官方中转分组密钥调用——请在控制台创建或切换为「官方中转」分组密钥")
			return
		}
	}
	if line != nil && line.Mode == "crowd" {
		// acu/ 众筹线（20260921 恢复）：请求前过**池子闸门**（池子有余额 + 用户当日配额未超），
		// 不预扣个人余额（池子是共享钱包，个人余额不受影响）。
		if st, code, msg := a.poolGate(actx.UserID); st != 0 {
			a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, code, st)
			errOut(w, st, code, msg)
			return
		}
		// 防刷：每用户在途并发上限（失败请求不占配额，无并发闸会让单用户无限打上游）
		rel, ok := guard.crowdEnter(actx.UserID)
		if !ok {
			a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "crowd_busy", 429)
			errOut(w, 429, "crowd_busy", "当前调用过于频繁（众筹模型每用户同时最多 3 路请求），请等待在途请求完成后再试")
			return
		}
		defer rel()
	}
	if unified {
		// 统一前缀：按密钥计费分组选线；未分组旧密钥按配置默认分组
		grp := actx.KeyGrp
		if grp == "" {
			grp = a.Cfg.Billing.DefaultGrp
		}
		if grp == "free" {
			// 纯免费分组密钥：收费模型全部拦截（免费模型走裸名不经此分支）
			errOut(w, 403, "free_grp_restricted", "当前密钥为纯免费分组，仅可调用免费模型（不带 aqua/ 前缀的裸模型名）；收费模型请在控制台将该密钥切换为按次/按量分组")
			return
		}
		if grp == "official" {
			// 官方中转分组密钥：官方中转通道已并入众筹池（tlk 线下架），引导直调众筹模型
			errOut(w, 403, "official_grp_scope", "官方中转通道已并入众筹池：任意密钥可直接调用众筹模型（acu/ 前缀，如 acu/glm-5.3，按次扣站点额度）；按量模型请使用按量分组密钥")
			return
		}
		line = a.lineForModel(grp, siteID) // 模型感知：分组默认线无此模型时在同模式其他线定位（如 gpt 线）
		if line == nil {
			if grp == "per_call" {
				// 按次线下架并入众筹池（admin_lines.enabled=0）：引导众筹池/按量分组
				errOut(w, 403, "per_call_suspended", "按次计费已并入众筹池：任意密钥可直接调用众筹模型（acu/ 前缀，按次扣站点额度）；按量模型请切换「按量计费」分组（免费模型不受影响）")
				return
			}
			// 20260919：笼统 503"未配置计费线"改为可行动的 404——错分组调模型是客户端侧可修复的
			errOut(w, 404, "model_not_found", "模型 "+req.Model+" 不在当前计费分组（"+grp+"）的可用范围：按次模型请用「免费 + 收费」分组密钥，按量模型请用「按量计费」分组密钥；可在控制台切换或新建对应分组密钥")
			return
		}
	}
	var model *config.Model
	for i := range line.Models {
		if line.Models[i].SiteID == siteID {
			model = &line.Models[i]
			break
		}
	}
	if model == nil {
		if unified {
			// 统一前缀分组路由下目标线没有该模型：明确告知换分组（按次/按量）可见
			errOut(w, 404, "model_not_found", "模型不存在："+req.Model+"（该模型不在当前密钥计费分组的可用列表，按量分组模型更全，可在控制台创建按量分组密钥）")
			return
		}
		errOut(w, 404, "model_not_found", "模型不存在："+req.Model)
		return
	}
	fullID := config.ModelFullName(line.ID, siteID)

	// 软下架（20260923 站长指令）：模型标记维护 → 503 拒绝。
	// 与"模型不存在"（404）刻意区分：维护是**临时的**，DB 记录/价目/密钥绑定全部保留，
	// 清标记即恢复——客户端据此提示"稍后再试"而非"换个模型"。
	// 检查点在预扣与落库之前，故不产生任何计费痕迹。
	if model.Maintenance {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "model_maintenance", 503)
		errOut(w, 503, "model_maintenance",
			"模型 "+req.Model+" 维护中，暂不可用（配置与数据已保留，恢复后即可继续调用）；其他模型不受影响")
		return
	}

	// 模型能力口径（20260920）：名义模型与上游真实模型能力对齐。
	// ① 纯文本模型拒收图片——宁可在网关侧给明确中文错误，也不把 image_url 转给不支持
	//    视觉的上游（上游原文报错用户看不懂，还会污染错误统计）；
	// ② 输出上限钳制——名义模型标称上限高于上游真实上限时（如 GLM-5.3-Flash 官方 128K）
	//    静默钳到真实值，避免"看起来支持 384K"的口径错位。
	if model.NoVision && hasImageContent(body) {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "vision_not_supported", 400)
		errOut(w, 400, "vision_not_supported",
			"模型 "+req.Model+" 为纯文本模型，不支持图片/视觉输入；如需图像理解请改用支持视觉的模型")
		return
	}

	// 上游模型名替换
	upBody, err := replaceModel(body, model.UpstreamID)
	if err != nil {
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}
	if upBody, err = clampMaxTokens(upBody, model.MaxOutputTokens); err != nil {
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}

	// 上游透传（20260922 站长指令）：**不做伪流式转换**——客户端请求体原样转发
	// （保留 stream / stream_options），上游响应直接回传，网关不插帧、不改写。
	//
	// 历史复盘：伪流式（上游非流式 → 网关转 SSE）曾用于规避流式硬切、提升计费精度，
	// 但代价是 kabuai 缓存命中率大幅下降（v4-flash 流式 91.0% → 非流式 0.0%、
	// v4-pro 93.8% → 78.7%），且会把"首字延迟"卖点变成整段生成时间。现已全线下线。

	// 价格组与生效价目（pricing 键 = 目标线全名，与密钥分组解耦）
	grp := billing.UserPriceGrp(a.DB.DB, actx.UserID, line.Mode)
	pricing, err := billing.CurrentPricing(a.DB.DB, fullID, grp)
	if err != nil {
		errOut(w, 500, "internal_error", "计费查询失败")
		return
	}
	if pricing == nil {
		errOut(w, 404, "model_not_found", "该模型已下架或暂不可用")
		return
	}
	// —— 2 号折扣钱包 · 强制模型-钱包划分（20260924 站长指令）——
	// 判据：该模型是否存在 pricing(model,'wallet2') 价目行。
	//   命中 → 该模型**只能**用 2 号钱包 + 折扣价（当前 = prime/TokenLinks 国模 8 个）
	//   未命中 → 主钱包 + 常规分组价（现状完全不变）
	// 用价目行而非硬编码线 ID：加一行 = 开通一个模型的折扣钱包权限，零代码改动；
	// 白名单与价格同源，不会出现"配了价但没开权限"或反之。
	wallet := billing.WalletMain
	if line.Mode == "per_token" {
		if w2, e := billing.CurrentPricing(a.DB.DB, fullID, "wallet2"); e == nil && w2 != nil {
			pricing, wallet, grp = w2, billing.WalletDiscount, "wallet2"
		}
	}

	// 请求落库（拿 rowid 供面值回写；resolved_line 记实际线，统计口径）
	rid := a.insertRequestLine(actx.UserID, actx.KeyHash, "chat", req.Model, req.Stream, line.ID)
	if rid == 0 {
		errOut(w, 500, "internal_error", "请求记录失败")
		return
	}

	// 预扣额：per_call = 单价；per_token = 输入估算 + max_tokens×输出价（保守上限），且 ≥ floor。
	// crowd（acu/ 众筹池）不预扣个人余额：池子共享钱包，请求前已过 poolGate 闸门
	prehold := int64(0)
	if line.Mode != "crowd" {
		prehold = a.preholdAmount(model, pricing, upBody)

		if err := billing.Prehold(a.DB.DB, actx.UserID, prehold, rid, wallet); err != nil {
			if errors.Is(err, billing.ErrAccountDisabled) {
				a.failRequest(rid, "account_disabled", 403)
				errOut(w, 403, "account_disabled", "账户已被禁用，请联系站长处理")
				return
			}
			if errors.Is(err, billing.ErrInsufficientBalance) {
				a.failRequest(rid, "insufficient_quota", 429) // 诊断 D2：Prehold 失败路径必须回写，不留 (empty) 盲区
				if wallet == billing.WalletDiscount {
					// 折扣钱包**绝不回落主钱包**（站长红线）：静默回落会按原价扣主钱包，
					// 用户以为还在打折 → 账目争议高发。明确拒绝并指路去充折扣钱包。
					errOut(w, 429, "insufficient_quota",
						"折扣钱包余额不足：该模型为折扣钱包专用（按折扣价结算），请到控制台「充值」页为折扣钱包独立充值后重试（两钱包资金独立，不支持互转）")
					return
				}
				errOut(w, 429, "insufficient_quota", "余额不足：使用收费模型须保持账户 0 元以上余额，请先到控制台充值（先付后用，绝不透支）")
				return
			}
			a.failRequest(rid, "prehold_error", 500)
			errOut(w, 500, "internal_error", "预扣失败")
			return
		}
		// 台账口径：本次实际扣费钱包落库（用量日志/对账据此区分主钱包与折扣钱包）
		if wallet == billing.WalletDiscount {
			_, _ = a.DB.Exec("UPDATE requests SET wallet=? WHERE rowid=?", wallet, rid)
		}
	}

	// 上游转发
	// 预算：非流式 300s；流式 600s（长文生成合理时长——旧口径 300s 会把仍在正常
	// 生成的长响应硬切断，上游侧正常完成计费，本站却表现为 stream_incomplete）
	client, cerr := a.clientFor(line.ID)
	if cerr != nil {
		// 线刚被热重载停用/删除：预扣全额退回，503 快速失败（旧实现此处 panic）
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "line_removed")
		a.failRequest(rid, "line_removed", 503)
		errOut(w, 503, "service_unavailable", "线路配置刚刚更新，请稍后重试")
		return
	}
	budget := 300 * time.Second
	if req.Stream {
		budget = 600 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	// 密钥配额消耗（P4）：defer 挂在最外层，两条 serve 路径（真流式/非流式）
	// 各自内部结算完成后统一在此累加配额——无需改动两个 serve 函数的签名。
	// 内部从 requests 回读实扣金额与计费状态，失败/退款请求不占配额。
	defer a.keyQuotaSettle(rid, actx.KeyID, keyQ)
	resp, key, err := client.DoKey(ctx, upBody, req.Stream, "/chat/completions", model.KeyIdx)
	if err != nil {
		log.Printf("[chat] 上游失败 model=%s line=%s err=%v", req.Model, line.ID, err)
		if ctxDone(r.Context()) || errors.Is(err, context.Canceled) {
			// 客户端在等待上游响应期间主动断开（刷新/取消/SDK 超时）：与模型健康无关，
			// 记 client_cancel（499），状态口径剔除，不再误判为模型故障
			a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "client_cancel")
			a.failRequest(rid, "client_cancel", 499)
			return
		}
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
		// 5xx 状态错误对付费线不计入秒败风暴（上游容量问题，非用户滥用；见 storm.go）
		if stormCountable(line.Mode, err) {
			stormFail(actx.KeyHash, stormPaidMode(line.Mode))
		}
		a.failRequest(rid, "upstream_error", 502)
		a.upstreamFailOut(w, line, err)
		return
	}
	// defer 绑定闭包而非当次 body：下方探测循环换钥重试会整体替换 resp（含 Body），
	// 固定 defer 只会关旧 body，重试成功后的新 body 将永不被关闭（连接泄漏）。
	// 每次换钥前旧 body 已在探测分支内显式 Close，最终响应由本闭包统一关闭。
	closeBody := func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	defer closeBody()
	stormReset(actx.KeyHash) // 上游拨号成功：清零秒败计数
	if key != nil {
		a.setRequestKeyIdx(rid, key.RawIdx) // codex 账号粒度记账：记实际使用的钥原始 idx（换号后以最终为准）
	}
	a.recordCodexUsage(line, key, resp) // 官方实时用量头落库（429 满额头也有价值）

	if resp.StatusCode != 200 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_"+fmt.Sprint(resp.StatusCode))
		a.failRequest(rid, "upstream_status", resp.StatusCode)
		// 信息隔离：上游报错转译为站点标准错误码，不透传原文
		upstreamErrOut(w, resp.StatusCode, eb)
		return
	}

	if req.Stream {
		// 首帧探测 + 断流换钥重试：上游 200 后、首字节前瞬断（排队失败/渠道抖动）时，
		// 客户端尚未收到任何内容 → 透明换钥重试，用户无感；首帧后断流属生成中断，无法重试
		for probe := 0; probe < 3; probe++ {
			br := bufio.NewReader(resp.Body)
			if _, perr := br.Peek(1); perr == nil {
				resp.Body = readerBody{Reader: br, Closer: resp.Body}
				break
			}
			resp.Body.Close()
			if probe == 2 {
				log.Printf("[chat] 首帧前断流 model=%s line=%s，重试耗尽", req.Model, line.ID)
				a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
				stormFail(actx.KeyHash, stormPaidMode(line.Mode))
				a.failRequest(rid, "first_frame_timeout", 502)
				errOut(w, 502, "first_frame_timeout", "上游已连接但未返回首帧（已自动重试 2 次仍失败），通常为瞬时抖动，请稍后重试")
				return
			}
			log.Printf("[chat] 首帧前断流 model=%s line=%s，换钥重试(%d/2)", req.Model, line.ID, probe+1)
			client.Pool.Advance()
			time.Sleep(300 * time.Millisecond)
			resp, key, err = client.DoKey(ctx, upBody, true, "/chat/completions", model.KeyIdx)
			if err != nil {
				log.Printf("[chat] 重试仍失败 model=%s line=%s err=%v", req.Model, line.ID, err)
				if ctxDone(r.Context()) || errors.Is(err, context.Canceled) {
					// 客户端在重试等待期间主动断开：与模型健康无关
					a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "client_cancel")
					a.failRequest(rid, "client_cancel", 499)
					return
				}
				a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
				if stormCountable(line.Mode, err) {
					stormFail(actx.KeyHash, stormPaidMode(line.Mode))
				}
				a.failRequest(rid, "upstream_error", 502)
				a.upstreamFailOut(w, line, err)
				return
			}
			if key != nil {
				a.setRequestKeyIdx(rid, key.RawIdx)
				a.recordCodexUsage(line, key, resp)
			}
			if resp.StatusCode != 200 {
				eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_"+fmt.Sprint(resp.StatusCode))
				a.failRequest(rid, "upstream_status", resp.StatusCode)
				upstreamErrOut(w, resp.StatusCode, eb)
				return
			}
		}
		a.serveStreamChat(w, r, resp, actx.UserID, prehold, rid, client, key, line, model, start, billing.EstPromptTokens(body), grp)
		return
	}
	a.serveJSONChat(w, resp, actx.UserID, prehold, rid, client, key, line, model, start, billing.EstPromptTokens(body), grp)
}

// paidLineForSiteID 裸名是否命中非免费线（收费/官方等计费线）的模型：
// 裸名兼容路由用——裸名撞收费线模型时优先走计费路径，绝不被免费线同名模型截胡。
// 读快照遍历（热重载在 linesMu 写锁内整体替换 slice，直读 a.Cfg.Lines 存在数据竞争）
func (a *App) paidLineForSiteID(siteID string) *config.Line {
	ls := a.linesSnap()
	for i := range ls {
		l := &ls[i]
		if l.Mode == "free" {
			continue
		}
		for j := range l.Models {
			if strings.EqualFold(l.Models[j].SiteID, siteID) {
				return l
			}
		}
	}
	return nil
}

// freeLineHasModel 该裸名是否由**免费线**提供（配置目录 admin_line_models + 动态目录 nvidia_models 都算）。
// 用途：裸名路由消歧（20260922）。同时存在于免费与收费目录的模型名（glm-5.3-flash / kimi-k3 /
// deepseek-v4.1-flash 等）一律按**免费**处理——/v1/models 里它们就是以免费模型示人的，
// 绝不能静默扣费。含已 retired 的条目：那种情况应返回 410 model_retired 明确告知，
// 而不是"悄悄按收费线跑一遍再扣钱"。
func (a *App) freeLineHasModel(name string) bool {
	if name == "" {
		return false
	}
	ls := a.linesSnap()
	for i := range ls {
		l := &ls[i]
		if l.Mode != "free" {
			continue
		}
		for j := range l.Models {
			if strings.EqualFold(l.Models[j].SiteID, name) {
				return true
			}
		}
		if l.Dynamic {
			var one int
			if a.DB.QueryRow("SELECT 1 FROM nvidia_models WHERE lower(id)=lower(?) LIMIT 1", name).Scan(&one) == nil {
				return true
			}
		}
	}
	return false
}

// upstreamFailOut 上游 Do/DoKey 失败统一转译（报错站点化：客户端只见站点中文错误码）。
// 全池纯配额耗尽（429 insufficient_quota）→ 503 line_exhausted 业务态（引导切线），
// 全池纯限流（429 tpm/rpm）→ 429 line_busy 业务态（不当故障处理）；
// 故障码细分（20260919：此前 ≥25 种故障共用 upstream_error，客户端/用户无法区分）：
//   - UPSTREAM_AUTH_DEAD → 502 upstream_auth_dead（GPT 账号池令牌链失效，已判死换号）
//   - UPSTREAM_STATUS_401/403 → 502 upstream_auth（上游鉴权异常）
//   - 其余 → 502 upstream_error 通用繁忙（上游原文绝不透传）
func (a *App) upstreamFailOut(w http.ResponseWriter, line *config.Line, derr error) {
	if derr != nil && strings.Contains(derr.Error(), "KEY_IDX_UNAVAILABLE") {
		// 分组隔离线：该模型绑定的专属钥当前不可用（冷却/判死），且无可替代钥
		// （同线其他钥属于不同上游分组，调本模型必失败）。给出可操作提示而非笼统"线路繁忙"。
		errOut(w, 503, "model_channel_unavailable",
			"该模型所属的专属通道当前暂不可用（上游限流或维护中），请稍后重试；其他模型不受影响")
		return
	}
	if derr != nil && strings.Contains(derr.Error(), "UPSTREAM_QUOTA_EXHAUSTED") {
		errOut(w, 503, "line_exhausted", line.Name+"本时段额度已用完，请改用其他模型或稍后再试")
		return
	}
	if derr != nil && (strings.Contains(derr.Error(), "UPSTREAM_RATE_LIMITED") || strings.Contains(derr.Error(), "UPSTREAM_STATUS_429")) {
		// 限流（含分组隔离线专属钥限流）：网关已内部重试至预算耗尽才到这里 → 业务态 429
		errOut(w, 429, "line_busy", line.Name+"当前访问过于火爆，请稍后重试或改用其他模型")
		return
	}
	if derr != nil && strings.Contains(derr.Error(), "UPSTREAM_AUTH_DEAD") {
		errOut(w, 502, "upstream_auth_dead", line.Name+"可用账号的授权已全部失效（失效账号已自动摘除），等待补充后自动恢复，请稍后再试")
		return
	}
	if derr != nil && (strings.Contains(derr.Error(), "UPSTREAM_STATUS_401") || strings.Contains(derr.Error(), "UPSTREAM_STATUS_403")) {
		errOut(w, 502, "upstream_auth", "上游鉴权异常，已通知站长处理，请稍后再试")
		return
	}
	errOut(w, 502, "upstream_error", "线路繁忙：已自动换线重试仍失败，请稍后重试")
}

// serveJSONChat 非流式：读全量 → 校验形状 → 剥层 → 结算 → 回写。
// inTokens = 客户端请求输入 token（billing.EstPromptTokens，20260923 标准化的统一口径）；
// 仅在上游未报 usage 时作为保守计费下界（20260919 亏本防线）。
// grp = 结算用的价格组（20260924 钱包化）：主钱包 → 用户分组价（normal/vip/agent）；
// 折扣钱包 → "wallet2"。**必须与预扣时同一组**，否则会出现"预扣按折扣价、结算按原价"
// 的双重口径（实测过：折扣钱包被按官方原价扣，用户多付一倍）。
func (a *App) serveJSONChat(w http.ResponseWriter, resp *http.Response, uid, prehold, rid int64, c *upstream.Client, key *upstream.KeyState, line *config.Line, m *config.Model, start time.Time, inTokens int64, grp string) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		a.settleFor(line, uid, prehold, 0, rid, 0, "read_error")
		a.failRequest(rid, "read_error", 502)
		errOut(w, 502, "upstream_error", "上游响应读取失败")
		return
	}
	// OpenAI 形状校验：上游 200 但响应不是合法 JSON 或缺 choices →
	// 视为上游畸形响应（透传会让客户端 SDK 报"格式错误"），转为 502 并全额退款
	var probe struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   *usageJSON        `json:"usage"`
		Error   json.RawMessage   `json:"error"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || (len(probe.Choices) == 0 && len(probe.Error) == 0) {
		a.settleFor(line, uid, prehold, 0, rid, 0, "upstream_malformed")
		a.failRequest(rid, "upstream_malformed", 502)
		errOut(w, 502, "upstream_error", "上游返回了格式异常的响应，请稍后重试")
		return
	}
	// 200 但 body 携带 error 对象（上游内部错误伪装 200）：此前照常走计费路径，
	// 用户会为失败请求买单——按 Do 失败同口径全额退款 + 502 业务态（与 malformed
	// 失败分支同格式；"error": null 视为无错误，不影响正常响应）
	if len(probe.Error) > 0 && string(probe.Error) != "null" {
		a.settleFor(line, uid, prehold, 0, rid, 0, "upstream_error")
		a.failRequest(rid, "upstream_error", 502)
		errOut(w, 502, "upstream_error", "上游返回了错误响应，请稍后重试")
		return
	}
	u := usageFromJSON(probe.Usage)
	final := int64(0)
	face := int64(0)
	// usage 有效性：必须至少一个维度 >0。上游返回 `"usage":{...全 0...}`（部分渠道在
	// 极短输出/缓存全命中时报 0）时，走 actual 分支会按 0 token 计费——**漏记**。
	// 20260919 计费审计：全 0 usage 视同无 usage，落保守估算路径（宁可多记不可少记）。
	hasUsage := probe.Usage != nil && (probe.Usage.PromptTokens > 0 || probe.Usage.CompletionTokens > 0)
	p, _ := billing.CurrentPricing(a.DB.DB, line.ID+"/"+m.SiteID, grp)
	switch {
	case p == nil:
		final = prehold // 价目缺失（活动价过期无历史行/被删）：服务已交付，fail-closed 绝不免费放行
	case p.Mode == "per_call":
		final = p.PriceMicro // 按次：不看 usage，收单价
	case hasUsage:
		final = billing.MeterTokens(u, p) // 按量有 usage：三段精算
	default:
		// 按量无 usage：按观测内容保守估算（20260919 亏本防线）——
		// 旧实现落 FloorMicro，上游实际产出的长文本会被按保底少收（实测少记约 5 倍）
		final = billing.MeterObserved(p, inTokens, jsonContentTokens(raw))
	}
	a.warnIfBelowCost(line, p, m, final)
	// 面值成本统一口径（按次取配置成本 / 按量按 usage 或观测估算）
	face = faceCostOf(p, m, u, hasUsage, inTokens, jsonContentTokens(raw))
	// 结算（多退少补）+ 面值台账 + 请求回写
	a.settleFor(line, uid, prehold, final, rid, final, "billed")
	if face > 0 && key != nil {
		c.Pool.ReportFace(key, face)
		_ = billing.LineKeyReport(a.DB.DB, line.ID, key.RawIdx, key.InitialMicro, face, rid)
	}
	src := "estimated"
	if hasUsage {
		src = "actual"
	}
	a.okRequest(rid, u, final, face, src, time.Since(start).Milliseconds(), 200)
	// 剥层后透传（信息隔离：移除成本/追踪类字段）
	out := stripSensitive(raw)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
}

// serveStreamChat SSE 流式：逐行转发 + 首尾事件计费。
// inTokens = 客户端请求输入 token（billing.EstPromptTokens，20260923 标准化统一口径）；
// 仅在上游未报 usage 时作为保守计费下界（20260919 亏本防线）。
// grp = 结算用的价格组（20260924 钱包化）：主钱包 → 用户分组价；折扣钱包 → "wallet2"。
// 必须与预扣同一组，否则"预扣折扣价、结算原价"会多扣用户一倍。
func (a *App) serveStreamChat(w http.ResponseWriter, r *http.Request, resp *http.Response, uid, prehold, rid int64, c *upstream.Client, key *upstream.KeyState, line *config.Line, m *config.Model, start time.Time, inTokens int64, grp string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.settleFor(line, uid, prehold, 0, rid, 0, "no_flusher")
		a.failRequest(rid, "no_flusher", 500)
		errOut(w, 500, "internal_error", "流式不可用")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	var u billing.Usage
	var firstByte time.Time // 首帧到达时间：tps 按生成阶段（首字之后）计，等待不计入生成速度
	final := int64(0)
	faceTotal := int64(0)
	settled := false
	sawDone := false     // 上游是否已发 [DONE]（未发即中断 → 客户端拿到的是截断流）
	interrupted := false // 上游异常中断（区别于客户端主动断开）
	truncated := false   // codex 流截断（已发部分内容+length 帧，ErrCodexTruncated 经 pipe 传来）：内容有效，保底计费
	// outTokens 已转发给客户端的正文 token 数（无 usage 时的保守计费下界，20260919 亏本防线）
	outTokens := int64(0)
	settle := func() {
		if settled {
			return
		}
		settled = true
		p, _ := billing.CurrentPricing(a.DB.DB, line.ID+"/"+m.SiteID, grp)
		hasUsage := u.PromptTokens > 0 || u.CompletionTokens > 0
		if hasUsage {
			if p != nil && p.Mode == "per_call" {
				// per_call：不看 usage，收单价
				final = p.PriceMicro
			} else if p != nil {
				final = billing.MeterTokens(u, p)
			} else {
				// 价目缺失：服务已交付，按预扣额收（fail-closed）
				final = prehold
			}
			// 面值成本统一口径（按次取配置成本，修复此前按次恒 0 的漏记）
			a.warnIfBelowCost(line, p, m, final)
			faceTotal = faceCostOf(p, m, u, true, inTokens, outTokens)
			if faceTotal > 0 && key != nil {
				c.Pool.ReportFace(key, faceTotal)
				_ = billing.LineKeyReport(a.DB.DB, line.ID, key.RawIdx, key.InitialMicro, faceTotal, rid)
			}
			a.settleFor(line, uid, prehold, final, rid, final, "billed")
			a.okRequestGen(rid, u, final, faceTotal, "actual", time.Since(start).Milliseconds(), 200, genMs(firstByte), ttftOf(firstByte, start))
		} else if interrupted {
			// 一帧有效内容都没有且上游异常中断：全额退回
			a.settleFor(line, uid, prehold, 0, rid, 0, "stream_incomplete")
			a.failRequest(rid, "stream_incomplete", 502)
		} else {
			// 上游正常收尾但未发 usage：按观测字节保守估算（亏本防线）——
			// 旧实现落 FloorMicro，实测少记约 5 倍；价目缺失时按预扣额收（fail-closed）。
			// per_call 不受影响（收单价，与 usage 无关）
			if p != nil {
				if p.Mode == "per_call" {
					final = p.PriceMicro
				} else {
					final = billing.MeterObserved(p, inTokens, outTokens)
				}
			} else {
				final = prehold
			}
			// 面值成本统一口径：上游确实消耗了（内容已产出），成本必须留痕
			a.warnIfBelowCost(line, p, m, final)
			faceTotal = faceCostOf(p, m, u, false, inTokens, outTokens)
			if faceTotal > 0 && key != nil {
				c.Pool.ReportFace(key, faceTotal)
				_ = billing.LineKeyReport(a.DB.DB, line.ID, key.RawIdx, key.InitialMicro, faceTotal, rid)
			}
			a.settleFor(line, uid, prehold, final, rid, final, "billed")
			a.okRequestGen(rid, u, final, faceTotal, "estimated", time.Since(start).Milliseconds(), 200, genMs(firstByte), ttftOf(firstByte, start))
		}
	}
	defer settle()
	ctx := r.Context()
	buf := make([]byte, 0, 32<<10)
	tmp := make([]byte, 16<<10)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			if firstByte.IsZero() {
				firstByte = time.Now()
			}
			buf = append(buf, tmp[:n]...)
			// 按 SSE 行处理：完整行才转发（便于剥层与 usage 抓取）
			for {
				i := bytes.IndexByte(buf, '\n')
				if i < 0 {
					break
				}
				lineBytes := buf[:i]
				buf = buf[i+1:]
				if isSSEDone(lineBytes) {
					sawDone = true
				}
				out := sanitizeStreamLine(lineBytes, &u)
				outTokens += contentTokens(lineBytes)
				_, _ = w.Write(out)
				_, _ = w.Write([]byte("\n"))
				flusher.Flush()
			}
			// 单行超限（上游无换行狂吐/异常）：完整行已转发，残留 buf 无上限增长会
			// 打爆内存——8MB 封顶，按上游中断收尾（未发 [DONE] 记 interrupted 走失败退款）
			if len(buf) > 8<<20 {
				log.Printf("[chat] 流式单行超过 8MB 上限 line=%s model=%s，按中断收尾", line.ID, m.SiteID)
				if !sawDone && !ctxDone(r.Context()) {
					interrupted = true
					errFrame := map[string]any{"error": map[string]any{
						"message": "上游流式响应中断，内容可能不完整，请重试",
						"type":    "api_error", "code": "stream_incomplete", "param": nil,
					}}
					eb, _ := json.Marshal(errFrame)
					_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", eb)
					flusher.Flush()
				}
				return
			}
		}
		if err != nil {
			// codex 流截断（有部分内容）：finish_reason=length 帧已转发、内容有效，
			// 不按异常中断处理（不补错误帧、结算不退款走保底计费），只补 [DONE] 收尾
			if errors.Is(err, upstream.ErrCodexTruncated) {
				truncated = true
			}
			// 上游中断且从未发过 [DONE]：补发一帧 OpenAI 错误事件再正常收尾，
			// 客户端 SDK 才能感知截断（否则表现为静默缺内容）
			if !sawDone && !truncated && !ctxDone(r.Context()) {
				interrupted = true
				errFrame := map[string]any{"error": map[string]any{
					"message": "上游流式响应中断，内容可能不完整，请重试",
					"type":    "api_error", "code": "stream_incomplete", "param": nil,
				}}
				eb, _ := json.Marshal(errFrame)
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", eb)
				flusher.Flush()
			}
			if truncated && !sawDone && !ctxDone(r.Context()) {
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
			return
		}
	}
}

// ctxDone 请求上下文是否已取消（客户端断开/超时：无需再写响应）
func ctxDone(ctx context.Context) bool {
	return ctx.Err() != nil
}

// isSSEDone SSE [DONE] 帧精确判定（与 sanitizeStreamLine 同口径：剥 data: 前缀后
// TrimSpace 精确比对——Contains 会把正文恰好含 "[DONE]" 字样的内容帧误判为流结束）
func isSSEDone(line []byte) bool {
	s := strings.TrimSpace(string(line))
	if strings.HasPrefix(s, "data:") {
		s = strings.TrimSpace(s[5:])
	}
	return s == "[DONE]"
}

// jsonContentTokens 统计非流式响应中正文（content / reasoning_content）的 token 数。
// 用途：上游未报 usage 时的保守计费下界（与 contentTokens 同口径）。
// 20260923 标准化：返回 token 数（billing.EstTokens，CJK 感知），不再返回字节数。
func jsonContentTokens(raw []byte) int64 {
	var jr struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &jr) != nil {
		return 0
	}
	var sb strings.Builder
	for i := range jr.Choices {
		sb.WriteString(jr.Choices[i].Message.Content)
		sb.WriteString(jr.Choices[i].Message.ReasoningContent)
	}
	if sb.Len() == 0 {
		return 0
	}
	return billing.EstTokens(sb.String())
}

// contentTokens 统计 SSE data 行中**正文增量**（content / reasoning_content）的 token 数。
// 用途：上游未报 usage 时的保守计费下界（20260919 亏本防线）——只数真正交付给用户的内容，
// 不数 JSON 骨架/元数据，避免把协议开销当 token 多收。
// 20260923 标准化：返回 token 数（billing.EstTokens，CJK 感知），不再返回字节数。
func contentTokens(line []byte) int64 {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return 0
	}
	payload := strings.TrimSpace(s[5:])
	if payload == "" || payload == "[DONE]" {
		return 0
	}
	var m struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(payload), &m) != nil {
		return 0
	}
	var sb strings.Builder
	for i := range m.Choices {
		sb.WriteString(m.Choices[i].Delta.Content)
		sb.WriteString(m.Choices[i].Delta.ReasoningContent)
		sb.WriteString(m.Choices[i].Message.Content)
		sb.WriteString(m.Choices[i].Message.ReasoningContent)
	}
	if sb.Len() == 0 {
		return 0
	}
	return billing.EstTokens(sb.String())
}

// sanitizeStreamLine SSE data 行处理：抓 usage（末帧）+ 剥除成本/计费/追踪类字段（信息隔离）
func sanitizeStreamLine(line []byte, u *billing.Usage) []byte {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return line
	}
	payload := strings.TrimSpace(s[5:])
	if payload == "" || payload == "[DONE]" {
		return line
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &m) != nil {
		return line
	}
	// usage 抓取（多个帧带 usage 时取最后一个非零值）。
	// 20260919 计费审计修复：原条件仅认 `prompt_tokens>0`——部分上游（如仅报输出侧的
	// 实现/中断前的 usage 帧）只给 completion_tokens，原逻辑会**整帧丢弃**该 usage，
	// 结算退化到 estimated 估算路径。改为任一维度 >0 即采纳（usageFromJSON 内部已做
	// cached<=prompt 防御），宁可多记不可少记。
	if ur, ok := m["usage"]; ok && ur != nil && string(ur) != "null" {
		var uj usageJSON
		if json.Unmarshal(ur, &uj) == nil && (uj.PromptTokens > 0 || uj.CompletionTokens > 0) {
			*u = usageFromJSON(&uj)
		}
	}
	// 剥层：绝不让上游成本/计费/追踪字段到达下游
	changed := false
	for _, k := range []string{"cost_cny", "billing_pending", "trace_id"} {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return line
	}
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return []byte("data: " + string(out))
}

// usageFromJSON JSON usage → billing.Usage（缓存命中兼容三种字段：OpenAI 标准结构 /
// DeepSeek 旧口径 prompt_cache_hit_tokens / Anthropic 口径 cache_read_input_tokens）
func usageFromJSON(j *usageJSON) billing.Usage {
	if j == nil {
		return billing.Usage{}
	}
	cached := int64(0)
	if j.PromptTokensDetails != nil {
		cached = j.PromptTokensDetails.CachedTokens
	}
	if cached == 0 {
		cached = j.PromptCacheHitTokens
	}
	if cached == 0 {
		cached = j.CacheReadInputTokens
	}
	if cached > j.PromptTokens {
		cached = j.PromptTokens // 防御：缓存命中不可能超过 prompt 总量
	}
	return billing.Usage{PromptTokens: j.PromptTokens, CompletionTokens: j.CompletionTokens, CachedTokens: cached}
}

// replaceModel 替换请求体中的 model 字段（保持其余字段原样）
func replaceModel(body []byte, newModel string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	mb, _ := json.Marshal(newModel)
	m["model"] = mb
	return json.Marshal(m)
}

// hasImageContent 检测 messages 中是否含图片内容块（image_url / input_image / image）。
// 纯文本模型（no_vision）入参拦截用。
func hasImageContent(body []byte) bool {
	var m struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	for _, msg := range m.Messages {
		if len(msg.Content) == 0 {
			continue
		}
		// content 为字符串（纯文本）时 Unmarshal 失败，跳过
		var parts []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg.Content, &parts); err != nil {
			continue
		}
		for _, p := range parts {
			switch p.Type {
			case "image_url", "input_image", "image":
				return true
			}
		}
	}
	return false
}

// clampMaxTokens 把请求体 max_tokens / max_completion_tokens 钳到 limit（limit<=0 表示不限）。
// 用 map[string]json.RawMessage 保真透传（除被钳字段外一字不动）；未超限时原样返回，不做无谓重编码。
func clampMaxTokens(body []byte, limit int64) ([]byte, error) {
	if limit <= 0 {
		return body, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	changed := false
	for _, k := range []string{"max_tokens", "max_completion_tokens"} {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil || v <= limit {
			continue
		}
		nb, _ := json.Marshal(limit)
		m[k] = nb
		changed = true
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(m)
}

// warnIfBelowCost 实收低于成本时的运行期亏本告警（20260919 计费审计）。
//
// 与播种/管理台的事前防线互补：这里是**事后兜底**——覆盖管理台直接改 DB、
// 活动价配置失误、成本率填错等绕过事前校验的场景。只告警不阻断（站长有权定价），
// 但会把事实写进日志 + 错误中心，让亏本不可能悄悄发生。
func (a *App) warnIfBelowCost(line *config.Line, p *billing.PricingInfo, m *config.Model, charged int64) {
	if p == nil || charged <= 0 {
		return
	}
	var cost int64
	switch {
	case m.Image:
		cost = m.PerImageCost // 图片按张：与 mode 无关，先判
	case p.Mode == "per_call":
		cost = m.PerCallCost
	default:
		return // 按量成本随 token 变化，逐笔比对无意义（由 IsBelowCost 在价目维度校验）
	}
	if cost <= 0 {
		return
	}
	// 含 3% 支付通道费：实收 × 0.97 须覆盖成本
	if charged*97 < cost*100 {
		log.Printf("[billing] ⚠️ 亏本告警：%s/%s 实收 %d 微元 < 成本 %d 微元（含 3%% 通道费，每笔亏 %d）",
			line.ID, m.SiteID, charged, cost, cost-charged)
		a.logError("below_cost", line.ID+"/"+m.SiteID, 0, 0,
			"charged="+strconv.FormatInt(charged, 10)+" cost="+strconv.FormatInt(cost, 10))
	}
}

// faceCostOf 面值成本口径统一（内部成本台账，绝不外泄）。
//
// 20260919 计费审计修复（成本漏记 → 利润统计虚高、掩盖真实亏损）：
//   - **按次（per_call）**：成本 = 配置的单次成本 `m.PerCallCost`。此前统一走 FaceCostMicro
//     （三段 rate10），而按次模型根本不配 rate10 成本字段 → 恒返回 0。
//     生产实测：aqua 线 33,754 笔 billed 中 32,763 笔 face_cost_micro=0（97% 漏记），
//     收入 2.73 亿微元对应的成本只记了 0.85 亿——利润被严重高估。
//   - 按量（per_token）有 usage：三段 rate10 精算（原口径不变）
//   - 按量无 usage：按观测字节保守估算（不虚构，宁多勿少）
func faceCostOf(p *billing.PricingInfo, m *config.Model, u billing.Usage, hasUsage bool, inTokens, outTokens int64) int64 {
	if p != nil && p.Mode == "per_call" {
		return m.PerCallCost
	}
	if hasUsage {
		return billing.FaceCostMicro(u, m.InCostRate10, m.CacheCostRate10, m.OutCostRate10)
	}
	return billing.FaceCostObserved(m.InCostRate10, m.OutCostRate10, inTokens, outTokens)
}

// stripSensitive 信息隔离：剥除上游响应中的成本/计费/追踪字段
func stripSensitive(raw []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	changed := false
	for _, k := range []string{"cost_cny", "billing_pending", "trace_id", "usage.cost_cny"} {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// preholdAmount 预扣额（one-api/new-api 同款门槛哲学：预扣只做防白嫖门槛，超支由 Settle 补扣兜底）：
// per_call=单次价；per_token=输入估算+max_tokens×输出价，≥floor。
//
// 20260919 计费审计修复（亏本防线，站长红线：宁可多记不可少记）：
//  1. 认 `max_completion_tokens`（OpenAI 新标准字段，新版 SDK / o 系列用它替代 max_tokens）——
//     此前只读 max_tokens，客户端只传该字段时预扣按兜底值算，长输出必超支；
//  2. 无输出上限时兜底 4096 → **8192**：主流客户端不传上限时输出常超 4096，
//     超支部分要等 Settle 追扣，若用户余额恰好被扣光即成站方亏损；
//  3. 兼容 completions 风格 `prompt` 字段（原仅读 messages，缺失时输入估算退化为 16）。
//  4. 20260923 标准化：输入估算改走 billing.EstPromptTokens（只数正文文本），
//     不再按 messages 原文 JSON 字节 /4。
//
// 上限 100000 对齐 new-api maxTokensLimit 防溢出。
func (a *App) preholdAmount(m *config.Model, p *billing.PricingInfo, body []byte) int64 {
	if p == nil {
		return 1000
	}
	if p.Mode == "per_call" {
		return p.PriceMicro
	}
	var req struct {
		MaxTokens           int64 `json:"max_tokens"`
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
	}
	_ = json.Unmarshal(body, &req)
	// 输入估算（20260923 标准化）：只统计正文文本 token（billing.EstPromptTokens），
	// 不再按 messages 原文 JSON 字节 /4 —— 旧口径把 role/content 字段名与引号括号
	// 都算成了 token，短消息密集的会话虚增数倍（预扣过高会把余额本就够用的用户拦在门外）。
	// 仍保留 1.2 保守余量：预扣只是防白嫖门槛，实际扣费以 Settle 精算为准。
	est := billing.EstPromptTokens(body)
	est = est * 6 / 5
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = req.MaxCompletionTokens // OpenAI 新标准字段
	}
	if maxOut <= 0 {
		maxOut = 8192
	}
	if maxOut > 100000 {
		maxOut = 100000
	}
	estCost := est*p.InRate10/10000 + maxOut*p.OutRate10/10000
	if estCost < p.FloorMicro {
		estCost = p.FloorMicro
	}
	return estCost
}

// insertRequest 请求落库，返回 rowid
func (a *App) insertRequest(uid int64, keyHash, endpoint, model string, stream bool) int64 {
	return a.insertRequestLine(uid, keyHash, endpoint, model, stream, "")
}

// insertRequestLine 请求落库（resolved_line 记实际计费线，统一前缀路由统计口径）
func (a *App) insertRequestLine(uid int64, keyHash, endpoint, model string, stream bool, resolvedLine string) int64 {
	res, err := a.DB.Exec(
		"INSERT INTO requests (key_hash, endpoint, model, user_id, ok, ts, bill_state, stream_mode, resolved_line) VALUES (?,?,?,?,0,?,?,?,?)",
		keyHash, endpoint, model, uid, time.Now().Unix(), "", boolToInt(stream), resolvedLine)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// setRequestKeyIdx 回写实际使用的钥原始 idx（codex 账号粒度用量/利润记账，换号后以最终为准；
// 与 admin_line_keys.idx 同一 DB 口径，非密钥池内序）
func (a *App) setRequestKeyIdx(rid int64, idx int) {
	if rid == 0 || idx < 0 {
		return
	}
	_, _ = a.DB.Exec("UPDATE requests SET key_idx=? WHERE rowid=?", idx, rid)
}

// okRequest 成功回写：usage + 金额 + 面值成本 + bill_state='billed'
// src=usage 来源口径（actual=上游实发 / estimated=保底估算）；latMs/sc 回写观测口径，
// tps=输出 tokens/秒（生成速度，非流式为总耗时口径）；消除生产旧表列默认值造成的统计失真
func (a *App) okRequest(rid int64, u billing.Usage, amount int64, face int64, src string, latMs int64, sc int) {
	a.okRequestGen(rid, u, amount, face, src, latMs, sc, latMs, 0)
}

// okRequestGen 成功回写（生成阶段口径）：genMs=生成阶段耗时（流式为首字之后；≤0 时回退总耗时）
// tps 按 genMs 计——等待（建连/prompt 处理/首字）不计入生成速度，latency_ms 仍按总耗时口径，
// ttftMs=首字延迟（请求起点→首帧到达；用户感知的"响应速度"，≤0 记 0，状态卡优先展示它）
func (a *App) okRequestGen(rid int64, u billing.Usage, amount int64, face int64, src string, latMs int64, sc int, genMs int64, ttftMs int64) {
	tpsMs := genMs
	if tpsMs <= 0 {
		tpsMs = latMs
	}
	tps := 0.0
	if tpsMs > 0 {
		tps = float64(u.CompletionTokens) * 1000 / float64(tpsMs)
	}
	_, _ = a.DB.Exec(
		"UPDATE requests SET ok=1, prompt_tokens=?, completion_tokens=?, cached_tokens=?, total_tokens=?, billed=1, bill_amount_micro=?, unit_price_micro=?, face_cost_micro=?, bill_state='billed', usage_source=?, latency_ms=?, status_code=?, tps=?, first_ms=? WHERE rowid=?",
		u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.PromptTokens+u.CompletionTokens, amount, amount, face, src, latMs, sc, tps, ttftMs, rid)
	// 邀请返利：异步队列结算（热路径零阻塞；acu/crowd 等范围过滤在结算器内，invite.go）
	a.inviteEnqueue(rid)
}

// genMs 生成阶段耗时（毫秒）：首帧未到达（无有效输出）返回 0，由调用方回退总耗时
func genMs(firstByte time.Time) int64 {
	if firstByte.IsZero() {
		return 0
	}
	return time.Since(firstByte).Milliseconds()
}

// ttftMs 首字延迟（毫秒）：请求起点→首帧到达；首帧未到达返回 0（非流式/无输出）
func ttftOf(firstByte, start time.Time) int64 {
	if firstByte.IsZero() {
		return 0
	}
	return firstByte.Sub(start).Milliseconds()
}

// failRequest 失败回写（诊断 D2：reason 必填区分失败原因，status_code 回写真实状态码供统计）
func (a *App) failRequest(rid int64, reason string, statusCode int) {
	_, _ = a.DB.Exec("UPDATE requests SET ok=0, error=?, status_code=?, bill_state='refunded' WHERE rowid=?", reason, statusCode, rid)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// clientFor 线客户端缓存（每线一个 KeyPool）。
// 线已被热重载停用/删除（调用方持有旧快照）时返回错误：调用方应 503 快速失败——
// 旧实现拿 nil 线构造客户端会直接 panic（请求连接被重置且上游无任何日志）。
func (a *App) clientFor(lineID string) (*upstream.Client, error) {
	a.clientsMu.Lock()
	defer a.clientsMu.Unlock()
	if a.clients == nil {
		a.clients = map[string]*upstream.Client{}
	}
	if c, ok := a.clients[lineID]; ok {
		return c, nil
	}
	l := a.lineByID(lineID)
	if l == nil {
		return nil, fmt.Errorf("line_removed(%s)", lineID)
	}
	c := upstream.NewClient(l)
	a.clients[lineID] = c
	return c, nil
}

// dbErr 检查（占位：统一错误检查）
func dbErr(err error) bool {
	return err != nil && err != sql.ErrNoRows
}
