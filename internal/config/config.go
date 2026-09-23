// Package config 实现四层配置加载与合并：
// 代码内置默认值 < config.toml < 环境变量(AQUA_*) < 命令行 flag。
// 红线：代码只含机制，不含事实——所有上游地址、密钥、成本率、站点信息
// 全部来自配置，代码与注释中不出现任何真实运营数据。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Site 站点信息（前端 /v1/meta 渲染源，全部可自定义）
type Site struct {
	Name        string `toml:"name"`
	Domain      string `toml:"domain"`
	DocsURL     string `toml:"docs_url"`
	QQGroup     string `toml:"qq_group"`
	QQGroupURL  string `toml:"qq_group_url"`
	QQGroup2    string `toml:"qq_group2"`
	QQGroupURL2 string `toml:"qq_group_url2"`
	// 计费倍率展示口径（仅宣传展示，不参与计费——计费以 pricing 表为准）：
	// rate_promo=当前促销倍率（如 "0.2"），rate_normal=促销结束后恢复倍率（如 "0.5"），
	// rate_promo_vip=VIP 促销倍率（如 "0.13"）——促销生效时 /v1/models 据此折算"原价"对照下发
	RatePromo    string `toml:"rate_promo"`
	RateNormal   string `toml:"rate_normal"`
	RatePromoVip string `toml:"rate_promo_vip"`
	// —— 自定义域名与证书（P5，20260919）——
	// 全部走配置，代码不含运营事实（域名/邮箱/路径）。
	CNAME      string `toml:"cname"`       // 用户应指向的 CNAME 目标（如 api.example.com），空=功能关闭
	ServerIP   string `toml:"server_ip"`   // 本站服务器 IP（A 记录校验用；空=仅校验 CNAME）
	ACMEEmail  string `toml:"acme_email"`  // Let's Encrypt 联系邮箱（空=禁用自动签发，仅允许用户自传证书）
	NginxDir   string `toml:"nginx_dir"`   // 动态 vhost 目录（如 /etc/nginx/conf.d）
	WebrootDir string `toml:"webroot_dir"` // ACME HTTP-01 挑战根目录（如 /data/aqua/acme-webroot）
	CertDir    string `toml:"cert_dir"`    // 用户上传证书存放根目录（如 /data/aqua/certs/custom）
	// 功能开关与上限
	DomainEnabled bool  `toml:"domain_enabled"` // 自定义域名功能总开关（关=接口返回 403 引导）
	DomainMaxPer  int64 `toml:"domain_max_per"` // 每用户域名上限（0=按 2 处理）
}

// Database 数据库配置
type Database struct {
	Driver string `toml:"driver"` // sqlite | postgres（预留）
	Path   string `toml:"path"`
}

// Model 模型映射：站内名 ↔ 上游名 + 成本率 + 售价率（rate10 整数口径）
type Model struct {
	SiteID          string `toml:"site_id"`
	UpstreamID      string `toml:"upstream_id"`
	Image           bool   `toml:"image"`          // 图片模型（按张计费）
	PerImageCost    int64  `toml:"per_image_cost"` // 成本（微元/张，内部台账）
	PerImageSell    int64  `toml:"per_image_sell"` // 售价（微元/张，普通组）
	InCostRate10    int64  `toml:"in_cost_rate10"` // 上游成本率（内部台账，绝不外泄）
	CacheCostRate10 int64  `toml:"cache_cost_rate10"`
	OutCostRate10   int64  `toml:"out_cost_rate10"`
	InSellRate10    int64  `toml:"in_sell_rate10"` // 站点售价率（普通组）
	CacheSellRate10 int64  `toml:"cache_sell_rate10"`
	OutSellRate10   int64  `toml:"out_sell_rate10"`
	PerCallCost     int64  `toml:"per_call_cost"`      // 按次线：成本（微元/次，保本线用）
	PerCallSell     int64  `toml:"per_call_sell"`      // 按次线：售价（微元/次，普通组）
	Degraded        bool   `toml:"degraded,omitempty"` // 降级标记（诊断 D4：上游故障时管理端手动标记，/v1/models 透出）
	// Maintenance 维护标记（20260923 站长指令：软下架）。与 Degraded 语义相反：
	// Degraded = 模型仍可用、仅对外透出"降级"提示；Maintenance = 模型对外**不可见且不可调用**
	// （/v1/models 不列出、调用返 503 model_maintenance），但 DB 记录（admin_line_models 行、
	// pricing 价目、密钥绑定）**全部保留**——这是"临时下架"而非"删除"，清标记即恢复。
	Maintenance bool `toml:"maintenance,omitempty"`
	// KeyIdx 模型专属密钥池序：-1=自动（走粘性池）；≥0 锁定"非 dead 密钥按 idx 排序后"的第 N 把（0 起）。
	// 用于专属钥按模型分工场景——注意 toml 种子零值即 0，走 toml 配置多钥分工时必须显式写 key_idx，线上以 DB 列为准。
	KeyIdx int64 `toml:"key_idx,omitempty"`
	// NoVision 纯文本模型（20260920）：拒绝图片/视觉输入（messages 含 image_url / input_image
	// → 400 vision_not_supported）。用于"名义模型支持视觉、实际上游模型不支持"的口径对齐场景。
	NoVision bool `toml:"no_vision"`
	// MaxOutputTokens 上游请求 max_tokens / max_completion_tokens 上限（20260920，0=不限）。
	// 用于模型官方输出上限低于上游默认值的场景（如 GLM-5.3-Flash 官方上限 128K）：
	// 用户传更大值时静默钳制，避免"看起来支持 384K"的口径错位。
	MaxOutputTokens int64 `toml:"max_output_tokens"`
}

// Line 一条上游线：独立的转发 + 计费体系。新增上游 = 追加一个 [[lines]] 块。
type Line struct {
	ID      string   `toml:"id"`   // 线标识（收费线模型形如 line-id/model；免费线模型为裸 site_id）
	Name    string   `toml:"name"` // 展示名
	Mode    string   `toml:"mode"` // per_call | per_token | free（免费线：不计费不预扣，仅转发记账）
	BaseURL string   `toml:"base_url"`
	Keys    []string `toml:"keys"`
	// KeyIdxs 与 Keys 一一对应的 DB 原始 idx（admin_line_keys.idx）：linesFromDB 装载时填充，
	// 专属钥定位与台账统一按原始 idx 口径（密钥池内序在剔除 dead/超卖钥后错位）；
	// TOML 静态配置线无 DB idx，留空（池内序即原始序）
	KeyIdxs []int `toml:"-"`
	// DeadKeyIdxs DB 标记为 dead 的原始 idx（admin_line_keys.dead=1）：
	// 装载时不再剔除（20260919），改为建池后标死标 + 30 分钟复活窗口，
	// 避免上游配额窗口刷新后死钥永久无法回归
	DeadKeyIdxs  []int  `toml:"-"`
	AuthStyle    string `toml:"auth_style"`     // bearer | x-api-key
	Proxy        string `toml:"proxy"`          // 出站代理（http:// 或 socks5://，仅该线转发走代理；空=直连）
	KeyFaceMicro int64  `toml:"key_face_micro"` // 每把密钥面值（微元，面值台账用；0=不限）
	Dynamic      bool   `toml:"dynamic"`        // 免费线：启用动态目录（nvidia_models 表，自动同步上游新模型）
	// Prefixed 免费线专属前缀展示：模型以 线id/site_id 形式透出（/v1/models、模型详情），
	// 调用侧前缀经 NormalizeModel 剥离后仍走免费分发——用于需要独立品牌框的免费线（如官方自营）。
	Prefixed bool `toml:"prefixed"`
	// KeyRPM 每钥每分钟请求上限（0=不限）：积分制上游（如商汤 Token Plan）按 key 限速，
	// 防单钥打爆触发上游 RPM/风控；建议正式号 6。
	KeyRPM int `toml:"key_rpm"`
	// StrictKeyIdx 严格专属钥模式（20260919）：模型配了 key_idx（≥0）时**硬绑定**该钥，
	// 任何情况下都不降级到通用池。
	//
	// 适用场景：**每把钥对应上游不同分组**的隔离线。例如 aqua 线两把钥——
	// 钥0 在 V4F代理分组（只有 deepseek-v4-flash / v4-pro），
	// 钥1 在 V4.1代理分组（只有 deepseek-v4-1-flash / glm-5.3 / glm-5.3-flash / kimi-k3）。
	// 此时若降级到"另一把钥"，必然因该分组不含此模型而失败
	// （No available channel for model X under group Y），白白浪费一次请求并拖长用户等待；
	// 生产实锤：未开严格模式时 glm-5.3 成功率仅 14.3%（一半请求先打到错钥再换）。
	//
	// 默认 false：同分组多把钥的线仍走"专属钥不可用则降级通用池"的容错（负载均衡）。
	StrictKeyIdx bool `toml:"strict_key_idx"`
	// AuthFailsToKill 连续鉴权失败（401/403）判死阈值（0=默认 3）：上游 IP 级风控会
	// 把"原本有效的 key"临时 401，立即判死会误杀全池——达阈值才判死，未达阈值走长冷却。
	AuthFailsToKill int `toml:"auth_fails_to_kill"`
	// StrictClean 转发前清洗请求体（剥 tools[].function.strict 等上游白名单外字段；
	// 针对请求体校验严格的积分制网关）
	StrictClean bool `toml:"strict_clean"`
	// vip 折扣 = 普通售价 × VipNum/VipDen（分数表达，整数可除零精度损失）
	VipNum int64   `toml:"vip_multiplier_num"`
	VipDen int64   `toml:"vip_multiplier_den"`
	Models []Model `toml:"models"`
}

// Server HTTP 服务配置
type Server struct {
	Listen string `toml:"listen"` // 如 0.0.0.0:8787
	// LegacyUpstreamURL 旧网关（Rust）地址：绞杀者迁移期免费线/未移植端点反代到此，
	// 共享同一 SQLite 库。置空 = 纯 Go 单体模式（非收费模型直接 404）。
	LegacyUpstreamURL string `toml:"legacy_upstream_url"`
}

// SMTP 邮箱验证码发信（注册/找回密码）
type SMTP struct {
	Host string `toml:"host"` // 如 smtpdm.aliyun.com
	Port int    `toml:"port"` // 通常 465（隐式 TLS）
	User string `toml:"user"` // 发信账号
	Pass string `toml:"pass"` // 发信密码/授权码
	From string `toml:"from"` // 发件人显示（默认同 User）
}

// EPay 易支付（充值渠道 V1，MD5 签名）
type EPay struct {
	Gateway    string `toml:"gateway"`     // 如 https://xnoo.cn
	PID        string `toml:"pid"`         // 商户 ID
	Key        string `toml:"key"`         // V1 商户密钥（MD5）
	NotifyBase string `toml:"notify_base"` // 回调域名（如 https://acu.example.com）
	ReturnBase string `toml:"return_base"` // 支付完成跳转域名
	// APIVersion 接口版本：v1（默认，MD5 + mapi.php/api.php）| v2（RSA + /api/pay/create|query）
	APIVersion string `toml:"api_version"`
	// PrivateKey V2 商户私钥（PKCS#8/PKCS#1 的 DER base64，或完整 PEM）——仅用于请求签名，绝不外泄/入库
	PrivateKey string `toml:"private_key"`
	// PlatformPub V2 平台公钥（base64 或 PEM）——用于验证平台响应与异步回调的签名
	PlatformPub string `toml:"platform_pub"`
}

// Pay 支付通道总配置（20260922 双通道）：
//   - 微信（wxpay）→ 走 Provider 指定的主通道；主通道是自挂时，异常会自动熔断降级到易支付
//   - 支付宝（alipay）→ 恒走易支付（自挂平台没有支付宝通道）
type Pay struct {
	// Provider 微信主通道：xiaofeng（自挂，钱进站长个人微信）| epay（易支付）
	// 空 = epay（保持旧行为）
	Provider string `toml:"provider"`
	// Xiaofeng 自建免签「自挂」通道（晓风/安逸云聚合支付）
	Xiaofeng Xiaofeng `toml:"xiaofeng"`
}

// Xiaofeng 自挂通道（个人免签：上传自己的个人微信收款码，靠挂机监控端上报到账）
//
// 实测口径（20260922 对生产网关 pay.61nb.vip 逐项验证）：
//   - 下单：POST mapi.php，MD5 签名，成功码 code=1，返回 payurl（收银台直链）
//   - 回调：**GET** query string，成功标识 trade_status=TRADE_SUCCESS，带 MD5 sign，
//     必须回纯文本 success；**平台不重试** → 对账轮询是硬需求
//   - 查单：GET /api/pay/result?trade_no= （匿名，status 0待付/1已付/2已过期）；
//     注意 api.php?act=order **不区分过期**，不能单独用它判断订单是否还活着
//   - 平台靠「通道 + 金额」匹配到账 → 同金额并存会错配，必须用金额互斥锁串行化
type Xiaofeng struct {
	Gateway    string `toml:"gateway"`     // 如 https://pay.61nb.vip
	PID        string `toml:"pid"`         // 商户 PID
	Key        string `toml:"key"`         // 商户密钥（MD5）——绝不入库/落日志/进仓库
	Type       string `toml:"type"`        // 支付类型，实测微信为 wxpay
	NotifyBase string `toml:"notify_base"` // 回调域名；空则回落 [epay].notify_base
	ReturnBase string `toml:"return_base"` // 回跳域名；空则回落 [epay].return_base
}

// Update 系统自动更新（双镜像：gitee 主仓库 + github 自动同步镜像）。
// 发版工作流：构建二进制提交进仓库 assets/ 目录 → 打 tag → push（gitee 自动同步 github），
// 服务器按 tag 从 github raw 拉取（海外畅通、匿名、版本可选）。
type Update struct {
	Repo       string `toml:"repo"`        // gitee 主仓库，如 xiaosu4610/acu-aqua（release 元数据 + release 附件源）
	MirrorRepo string `toml:"mirror_repo"` // github 镜像仓库，如 xiaosu4610/ACU-AQUA（服务器侧主源：tags + raw 下载）
	Token      string `toml:"token"`       // gitee 私有仓库访问令牌（公开仓库可空）
	AssetName  string `toml:"asset_name"`  // 发行版附件名（linux-amd64 二进制），默认 aqua-gateway-go-linux-amd64
	AssetDir   string `toml:"asset_dir"`   // 仓库内附件目录（raw 下载路径前缀），默认 assets
	Service    string `toml:"service"`     // systemd 服务名（更新后自重启），默认 aqua-gateway-go
	Enabled    bool   `toml:"enabled"`     // 是否启用在线更新入口（默认关；启用后管理后台才显示检查更新）
	APIBase    string `toml:"api_base"`    // gitee v5 API 地址（默认官方；服务器无法直连 gitee 时可指向国内跳板反代）
	Proxy      string `toml:"proxy"`       // 出站代理（http://host:port），更新请求走代理（海外服务器被 gitee CDN 拦截时使用）
}

// Billing 收费模型统一路由（机制层）：
// 统一前缀下（如 aqua/模型名）不再绑定单一计费线，而是按**密钥的计费分组**
// 路由到对应模式（per_call/per_token）的收费线；旧线前缀（如 tide/）仍可显式直连。
type Billing struct {
	UnifiedPrefix string `toml:"unified_prefix"` // 统一收费前缀；空 = 关闭统一路由（线前缀直连）
	DefaultGrp    string `toml:"default_grp"`    // 未分组旧密钥的默认计费分组：per_call | per_token
}

// Cfg 顶层配置
type Cfg struct {
	Site     Site     `toml:"site"`
	Database Database `toml:"database"`
	Server   Server   `toml:"server"`
	SMTP     SMTP     `toml:"smtp"`
	EPay     EPay     `toml:"epay"`
	Pay      Pay      `toml:"pay"`
	Update   Update   `toml:"update"`
	Billing  Billing  `toml:"billing"`
	Lines    []Line   `toml:"lines"`
}

// Default 内置默认值（全部为安全占位，不含任何运营事实）
func Default() *Cfg {
	return &Cfg{
		Site:     Site{Name: "AQUA api", Domain: "", DocsURL: "", QQGroup: "", QQGroupURL: ""},
		Database: Database{Driver: "sqlite", Path: "data/aqua.db"},
		Server:   Server{Listen: "0.0.0.0:8787"},
		Billing:  Billing{UnifiedPrefix: "", DefaultGrp: "per_call"}, // 统一前缀默认关闭，由配置显式启用
		Lines:    nil,                                                // 未配置任何上游 = 空壳站，UI 显示待配置
	}
}

// Load 加载顺序：默认值 → toml 文件（若存在）→ 环境变量覆盖
func Load(path string) (*Cfg, error) {
	c := Default()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, c); err != nil {
				return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
			}
		}
	}
	applyEnv(c)
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// applyEnv 环境变量覆盖（AQUA_ 前缀，机密类推荐只用 ENV 不落盘）
func applyEnv(c *Cfg) {
	if v := os.Getenv("AQUA_LISTEN"); v != "" {
		c.Server.Listen = v
	}
	if v := os.Getenv("AQUA_LEGACY_UPSTREAM_URL"); v != "" {
		c.Server.LegacyUpstreamURL = v
	}
	if v := os.Getenv("AQUA_DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("AQUA_SITE_NAME"); v != "" {
		c.Site.Name = v
	}
	if v := os.Getenv("AQUA_SITE_DOMAIN"); v != "" {
		c.Site.Domain = v
	}
	if v := os.Getenv("AQUA_SITE_DOCS_URL"); v != "" {
		c.Site.DocsURL = v
	}
	if v := os.Getenv("AQUA_SITE_QQ_GROUP"); v != "" {
		c.Site.QQGroup = v
	}
	if v := os.Getenv("AQUA_SITE_QQ_GROUP_URL"); v != "" {
		c.Site.QQGroupURL = v
	}
	if v := os.Getenv("AQUA_SITE_QQ_GROUP2"); v != "" {
		c.Site.QQGroup2 = v
	}
	if v := os.Getenv("AQUA_SITE_QQ_GROUP_URL2"); v != "" {
		c.Site.QQGroupURL2 = v
	}
	if v := os.Getenv("AQUA_SMTP_HOST"); v != "" {
		c.SMTP.Host = v
	}
	if v := os.Getenv("AQUA_SMTP_USER"); v != "" {
		c.SMTP.User = v
	}
	if v := os.Getenv("AQUA_SMTP_PASS"); v != "" {
		c.SMTP.Pass = v
	}
	if n := envInt("AQUA_SMTP_PORT", 0); n > 0 {
		c.SMTP.Port = int(n)
	}
	if v := os.Getenv("AQUA_EPAY_GATEWAY"); v != "" {
		c.EPay.Gateway = v
	}
	if v := os.Getenv("AQUA_EPAY_PID"); v != "" {
		c.EPay.PID = v
	}
	if v := os.Getenv("AQUA_EPAY_KEY"); v != "" {
		c.EPay.Key = v
	}
	if v := os.Getenv("AQUA_EPAY_NOTIFY_BASE"); v != "" {
		c.EPay.NotifyBase = v
	}
	if v := os.Getenv("AQUA_EPAY_RETURN_BASE"); v != "" {
		c.EPay.ReturnBase = v
	}
	// 自挂通道（微信主通道）：密钥只走环境变量/配置文件，绝不入库、不落日志、不进仓库
	if v := os.Getenv("AQUA_PAY_PROVIDER"); v != "" {
		c.Pay.Provider = v
	}
	if v := os.Getenv("AQUA_XF_GATEWAY"); v != "" {
		c.Pay.Xiaofeng.Gateway = v
	}
	if v := os.Getenv("AQUA_XF_PID"); v != "" {
		c.Pay.Xiaofeng.PID = v
	}
	if v := os.Getenv("AQUA_XF_KEY"); v != "" {
		c.Pay.Xiaofeng.Key = v
	}
	if v := os.Getenv("AQUA_XF_TYPE"); v != "" {
		c.Pay.Xiaofeng.Type = v
	}
	if v := os.Getenv("AQUA_UPDATE_REPO"); v != "" {
		c.Update.Repo = v
	}
	if v := os.Getenv("AQUA_UPDATE_TOKEN"); v != "" {
		c.Update.Token = v
	}
	if v := os.Getenv("AQUA_UPDATE_ASSET_NAME"); v != "" {
		c.Update.AssetName = v
	}
	if v := os.Getenv("AQUA_UPDATE_SERVICE"); v != "" {
		c.Update.Service = v
	}
	if v := os.Getenv("AQUA_UPDATE_ENABLED"); v == "1" || strings.EqualFold(v, "true") {
		c.Update.Enabled = true
	}
}

// validate 配置合法性校验
func (c *Cfg) validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen 不能为空")
	}
	if c.Database.Driver != "sqlite" {
		return fmt.Errorf("database.driver 目前仅支持 sqlite（%s）", c.Database.Driver)
	}
	seen := map[string]bool{}
	for i := range c.Lines {
		l := &c.Lines[i]
		if l.ID == "" {
			return fmt.Errorf("lines[%d].id 不能为空", i)
		}
		if seen[l.ID] {
			return fmt.Errorf("lines[%d].id 重复：%s", i, l.ID)
		}
		seen[l.ID] = true
		if l.BaseURL == "" {
			return fmt.Errorf("line %s 缺少 base_url", l.ID)
		}
		if l.Mode != "per_call" && l.Mode != "per_token" && l.Mode != "free" &&
			l.Mode != "official" && l.Mode != "crowd" {
			return fmt.Errorf("line %s mode 必须是 per_call、per_token、free、official 或 crowd", l.ID)
		}
		if l.AuthStyle == "" {
			l.AuthStyle = "bearer"
		}
		if l.Mode == "per_token" && l.VipDen == 0 {
			l.VipNum, l.VipDen = 1, 1 // 未配置 vip 折扣 = 不打折
		}
		for j := range l.Models {
			m := &l.Models[j]
			if m.SiteID == "" {
				return fmt.Errorf("line %s models[%d].site_id 不能为空", l.ID, j)
			}
			if m.UpstreamID == "" {
				m.UpstreamID = m.SiteID
			}
		}
	}
	return nil
}

// LineByID 按线 ID 查找
func (c *Cfg) LineByID(id string) *Line {
	for i := range c.Lines {
		if c.Lines[i].ID == id {
			return &c.Lines[i]
		}
	}
	return nil
}

// LineForMode 取第一条指定计费模式的收费线（统一前缀分组路由的目标线）。
// 多条同模式线时按配置顺序取第一条——分组路由只关心计费方式，具体线路是配置事实。
func (c *Cfg) LineForMode(mode string) *Line {
	for i := range c.Lines {
		if c.Lines[i].Mode == mode {
			return &c.Lines[i]
		}
	}
	return nil
}

// NormalizeBillingGrp 计费分组标准化：per_call / per_token / free（纯免费，仅可调免费模型），其余归空（未分组）
func NormalizeBillingGrp(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "per_call":
		return "per_call"
	case "per_token":
		return "per_token"
	case "free":
		return "free"
	}
	return ""
}

// ModelFullName 站内完整模型名（line-id/site-id）
func ModelFullName(lineID, siteID string) string {
	return lineID + "/" + siteID
}

// SplitModel 拆解站内模型名 → (lineID, siteID)；非收费线模型返回 ok=false
func SplitModel(full string) (lineID, siteID string, ok bool) {
	i := strings.IndexByte(full, '/')
	if i <= 0 || i >= len(full)-1 {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}

// FindFreeModel 跨免费线按裸 site_id 查模型（大小写不敏感；免费模型无 line-id/ 前缀）。
// lines 传调用方快照（App.linesSnap()）：热重载整体替换 Cfg.Lines，直接遍历 c.Lines 存在数据竞争
func FindFreeModel(lines []Line, siteID string) (*Line, *Model) {
	for i := range lines {
		l := &lines[i]
		if l.Mode != "free" {
			continue
		}
		for j := range l.Models {
			if strings.EqualFold(l.Models[j].SiteID, siteID) {
				return l, &l.Models[j]
			}
		}
	}
	return nil, nil
}

// IsAutoModel auto 智能路由模型判定（含旧 ID 兼容）
func IsAutoModel(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "auto", "acu/auto", "acu/auto-models":
		return true
	}
	return false
}

// NormalizeModel 旧模型 ID → 新 ID 兼容：auto 特例 + 去首个「厂商/」前缀段 + 全小写
// （例：zhipu/glm-4-flash → glm-4-flash；仅用于免费模型，收费线走 SplitModel）
func NormalizeModel(m string) string {
	t := strings.TrimSpace(m)
	if IsAutoModel(t) {
		return "auto"
	}
	if i := strings.IndexByte(t, '/'); i >= 0 {
		return strings.ToLower(t[i+1:])
	}
	return strings.ToLower(t)
}

// envInt 环境变量读整数（内部工具）
func envInt(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
