// domains.go 自定义域名与证书（P5，20260919）。
//
// 用户流程：提交域名 → 加 CNAME 指向本站 → DNS 校验（可选"测试并添加"）
//
//	→ 证书（用户自传 / 平台 LE 自动签发）→ 生成 nginx vhost → reload → 生效。
//
// 安全红线：
//   - 域名严格正则白名单（防 nginx 配置注入）
//   - 禁止本站主域名 / IP 字面量 / localhost / 保留域名
//   - 证书私钥权限 0600，不写日志、不返回前端、不落 DB（只存路径）
//   - 写 nginx 配置前先 `nginx -t`，失败回滚
//
// 全部路径/域名/邮箱走配置（代码不含运营事实）。
package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
)

// domainRe 域名白名单正则（严格：仅小写字母/数字/连字符，点分至少两段）
// 用于**防 nginx 配置注入**——域名会进入 nginx server_name 与文件路径。
var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// reservedSuffix 禁止注册的域名后缀（本站主域名，避免用户把主站指到自己的 vhost）
var reservedSuffix = []string{".ltzy.top", ".zhuafs.com"}

// domainFeatureOn 功能开关（未配置 CNAME 或显式关闭 → 不可用）
func (a *App) domainFeatureOn() bool {
	return a.Cfg.Site.DomainEnabled && strings.TrimSpace(a.Cfg.Site.CNAME) != ""
}

// domainMaxPer 每用户域名上限（默认 2，与站长定稿一致）
func (a *App) domainMaxPer() int64 {
	if a.Cfg.Site.DomainMaxPer > 0 {
		return a.Cfg.Site.DomainMaxPer
	}
	return 2
}

// validDomain 域名合法性 + 安全校验（返回错误说明，空串=通过）
func (a *App) validDomain(d string) string {
	if len(d) < 4 || len(d) > 253 {
		return "域名长度不合法"
	}
	if !domainRe.MatchString(d) {
		return "域名格式不合法：仅允许小写字母、数字、连字符与点（如 api.yourdomain.com）"
	}
	if net.ParseIP(d) != nil {
		return "不支持 IP 地址，请使用域名"
	}
	low := strings.ToLower(d)
	if low == "localhost" || strings.HasSuffix(low, ".localhost") {
		return "不支持 localhost"
	}
	if !strings.Contains(low, ".") {
		return "请提供完整域名（含顶级域，如 api.yourdomain.com）"
	}
	for _, suf := range reservedSuffix {
		if strings.HasSuffix(low, suf) {
			return "该域名属于本站保留域名，不可绑定"
		}
	}
	return ""
}

// dnsVerify 校验用户域名是否已正确指向本站（"测试并添加"与后续重试共用）。
// 通过条件（满足其一）：CNAME 链命中配置的 CNAME 目标；A 记录命中配置的服务器 IP。
// 返回 (通过, 方式, 详情说明)——详情用于前端展示实际解析结果，便于用户排查。
func (a *App) dnsVerify(domain string) (bool, string, string) {
	target := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a.Cfg.Site.CNAME)), ".")
	// 1) CNAME 链
	if cname, err := net.LookupCNAME(domain); err == nil {
		c := strings.TrimSuffix(strings.ToLower(cname), ".")
		if c != "" && c != strings.ToLower(domain) && target != "" {
			// 允许直接命中，或链路中最终落到目标域（LookupCNAME 已返回最终目标）
			if c == target || strings.HasSuffix(c, "."+target) {
				return true, "cname", "CNAME 已指向 " + a.Cfg.Site.CNAME
			}
		}
	}
	// 2) A 记录命中本站 IP
	ips, err := net.LookupHost(domain)
	if err != nil {
		return false, "", "DNS 解析失败：" + err.Error() + "（请确认域名已添加 CNAME 记录，通常 1~10 分钟生效）"
	}
	serverIP := strings.TrimSpace(a.Cfg.Site.ServerIP)
	detail := "当前解析结果：" + strings.Join(ips, ", ")
	if serverIP != "" {
		for _, ip := range ips {
			if ip == serverIP {
				return true, "a", "A 记录已指向本站服务器 " + serverIP
			}
		}
		detail += "（本站服务器为 " + serverIP + "）"
	}
	if target != "" {
		detail += "；请添加 CNAME：" + domain + " → " + a.Cfg.Site.CNAME
	}
	return false, "", "未检测到指向本站的解析记录。" + detail
}

// ---- 接口实现 ----

// myDomainsGet GET /v1/my/domains → 我的域名列表（含状态/证书到期/校验结果）
func (a *App) myDomainsGet(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	rows, err := a.DB.Query(
		`SELECT id, domain, status, COALESCE(verify_method,''), COALESCE(dns_checked_ts,0),
		        COALESCE(cert_type,''), COALESCE(cert_expires_ts,0), COALESCE(last_error,''),
		        COALESCE(created_ts,0), COALESCE(activated_ts,0)
		 FROM custom_domains WHERE user_id=? ORDER BY created_ts DESC`, actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	now := time.Now().Unix()
	for rows.Next() {
		var id, checked, certExp, created, activated int64
		var domain, status, method, certType, lastErr string
		if rows.Scan(&id, &domain, &status, &method, &checked, &certType, &certExp, &lastErr, &created, &activated) != nil {
			continue
		}
		item := map[string]any{
			"id": id, "domain": domain, "status": status,
			"verify_method": method, "dns_checked_ts": checked,
			"cert_type": certType, "cert_expires_ts": certExp,
			"last_error": lastErr, "created_ts": created, "activated_ts": activated,
		}
		if certExp > 0 {
			item["cert_days_left"] = (certExp - now) / 86400 // 证书剩余天数（前端临期高亮）
		}
		items = append(items, item)
	}
	jsonOut(w, 200, map[string]any{
		"domains":      items,
		"enabled":      a.domainFeatureOn(),
		"cname":        a.Cfg.Site.CNAME,
		"max":          a.domainMaxPer(),
		"le_available": strings.TrimSpace(a.Cfg.Site.ACMEEmail) != "", // 平台自动签发是否可用
	})
}

// myDomainsCreate POST /v1/my/domains {domain, test} → 添加域名
// test=true 即"测试并添加"：先做 DNS 校验，通过才入库为 pending（并立即尝试签发/激活）。
func (a *App) myDomainsCreate(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	if !a.domainFeatureOn() {
		errOut(w, 403, "domain_disabled", "自定义域名功能当前未开放，请联系站长")
		return
	}
	var req struct {
		Domain string `json:"domain"`
		Test   bool   `json:"test"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	domain = strings.TrimSuffix(domain, ".")
	if msg := a.validDomain(domain); msg != "" {
		errOut(w, 400, "bad_request", msg)
		return
	}
	// 上限校验（防滥用）
	var cnt int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM custom_domains WHERE user_id=?", actx.UserID).Scan(&cnt)
	if cnt >= a.domainMaxPer() {
		errOut(w, 400, "domain_limit", fmt.Sprintf("每个账号最多绑定 %d 个自定义域名：请先删除不再使用的域名", a.domainMaxPer()))
		return
	}
	// 全局唯一（别人已绑定则拒绝）
	var owner int64
	if err := a.DB.QueryRow("SELECT user_id FROM custom_domains WHERE domain=?", domain).Scan(&owner); err == nil {
		if owner == actx.UserID {
			errOut(w, 409, "conflict", "该域名你已添加过")
		} else {
			errOut(w, 409, "conflict", "该域名已被其他账号绑定")
		}
		return
	}
	// DNS 校验（test=true 时强校验；直接添加则入库 pending，后续可重试校验）
	method, checkedAt := "", int64(0)
	if req.Test {
		ok, m, detail := a.dnsVerify(domain)
		if !ok {
			errOut(w, 400, "dns_not_verified", detail)
			return
		}
		method, checkedAt = m, time.Now().Unix()
	}
	now := time.Now().Unix()
	res, err := a.DB.Exec(
		`INSERT INTO custom_domains (user_id, domain, status, verify_method, dns_checked_ts, created_ts)
		 VALUES (?,?,?,?,?,?)`,
		actx.UserID, domain, "pending", method, checkedAt, now)
	if err != nil {
		errOut(w, 500, "internal_error", "添加失败")
		return
	}
	id, _ := res.LastInsertId()
	a.auditAppend("domain_add", actx.UserID, "domain="+domain, clientIP(r))
	jsonOut(w, 200, map[string]any{
		"ok": true, "id": id, "domain": domain, "status": "pending",
		"message": "域名已添加。下一步请上传证书或使用平台自动签发，然后点击「校验并激活」。",
	})
}

// myDomainsVerify POST /v1/my/domains/{id}/verify → 重新校验 DNS（并可激活）
func (a *App) myDomainsVerify(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	domain, ok := a.ownDomain(actx.UserID, id)
	if !ok {
		errOut(w, 404, "not_found", "域名不存在或不属于你")
		return
	}
	pass, method, detail := a.dnsVerify(domain)
	now := time.Now().Unix()
	if !pass {
		_, _ = a.DB.Exec("UPDATE custom_domains SET dns_checked_ts=?, last_error=?, status='failed' WHERE id=?",
			now, detail, id)
		errOut(w, 400, "dns_not_verified", detail)
		return
	}
	_, _ = a.DB.Exec("UPDATE custom_domains SET dns_checked_ts=?, verify_method=?, last_error='' WHERE id=?",
		now, method, id)
	// DNS 通过 → 若已有证书则直接激活（生成 nginx vhost + reload）
	activated, actErr := a.activateDomain(id)
	jsonOut(w, 200, map[string]any{
		"ok": true, "verified": true, "method": method, "detail": detail,
		"activated": activated, "activate_error": actErr,
		"message": func() string {
			if activated {
				return "DNS 校验通过，域名已生效：" + domain
			}
			if actErr != "" {
				return "DNS 校验通过，但激活失败：" + actErr
			}
			return "DNS 校验通过。请先上传证书或使用平台自动签发，再点击「校验并激活」。"
		}(),
	})
}

// myDomainsCertUpload POST /v1/my/domains/{id}/cert/upload {fullchain, privkey}
// 用户自传证书：校验私钥匹配 + 域名匹配 + 未过期，落盘 0600 后激活。
func (a *App) myDomainsCertUpload(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	domain, ok := a.ownDomain(actx.UserID, id)
	if !ok {
		errOut(w, 404, "not_found", "域名不存在或不属于你")
		return
	}
	var req struct {
		Fullchain string `json:"fullchain"`
		Privkey   string `json:"privkey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Fullchain) == "" || strings.TrimSpace(req.Privkey) == "" {
		errOut(w, 400, "bad_request", "请同时提供证书链（fullchain）与私钥（privkey）内容")
		return
	}
	certPEM := []byte(req.Fullchain)
	keyPEM := []byte(req.Privkey)
	// 校验：私钥与证书匹配
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		errOut(w, 400, "bad_cert", "证书与私钥不匹配或格式错误："+err.Error())
		return
	}
	// 校验：证书含该域名 + 未过期
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		errOut(w, 400, "bad_cert", "证书解析失败："+err.Error())
		return
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		errOut(w, 400, "bad_cert", "证书不包含域名 "+domain+"："+err.Error())
		return
	}
	if time.Now().After(leaf.NotAfter) {
		errOut(w, 400, "bad_cert", "证书已过期（到期时间 "+leaf.NotAfter.Format("2006-01-02")+"）")
		return
	}
	// 落盘（私钥 0600；不写日志、不返回、不落 DB）
	dir := filepath.Join(a.Cfg.Site.CertDir, safeDomainFile(domain))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		errOut(w, 500, "internal_error", "证书目录创建失败")
		return
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		errOut(w, 500, "internal_error", "证书写入失败")
		return
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		errOut(w, 500, "internal_error", "私钥写入失败")
		return
	}
	_, _ = a.DB.Exec(
		`UPDATE custom_domains SET cert_type='upload', cert_path=?, key_path=?, cert_expires_ts=?, last_error='' WHERE id=?`,
		certPath, keyPath, leaf.NotAfter.Unix(), id)
	a.auditAppend("domain_cert_upload", actx.UserID, "domain="+domain, clientIP(r))
	activated, actErr := a.activateDomain(id)
	jsonOut(w, 200, map[string]any{
		"ok": true, "activated": activated, "activate_error": actErr,
		"cert_expires_ts": leaf.NotAfter.Unix(),
		"message": func() string {
			if activated {
				return "证书已生效，域名可用了：" + domain
			}
			return "证书已保存，但激活失败：" + actErr
		}(),
	})
}

// myDomainsCertIssue POST /v1/my/domains/{id}/cert/issue → 平台自动签发（Let's Encrypt）
// 前置：DNS 已校验通过（ACME HTTP-01 需要能访问到本站 80 端口）。
// 限流：同域名 24h 内不重复申请（LE 官方每域名每周 50 张）。
func (a *App) myDomainsCertIssue(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	if strings.TrimSpace(a.Cfg.Site.ACMEEmail) == "" {
		errOut(w, 403, "acme_disabled", "平台自动签发当前未开放，请改用「上传自己的证书」")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	domain, ok := a.ownDomain(actx.UserID, id)
	if !ok {
		errOut(w, 404, "not_found", "域名不存在或不属于你")
		return
	}
	// 签发前置：DNS 必须已通过（否则 ACME 验证必然失败，浪费 LE 配额）
	if pass, _, detail := a.dnsVerify(domain); !pass {
		errOut(w, 400, "dns_not_verified", "签发前必须确认 DNS 已生效："+detail)
		return
	}
	// ACME 联系邮箱＝**用户自己账户绑定的邮箱**（站长 20260920 指令）；
	// 为空/非法时兜底平台邮箱（否则 certbot 会因无效邮箱拒绝注册）。
	cfgDir := a.acmeConfigDir(actx.UserID)
	email := a.userACMEEmail(actx.UserID)
	emailFrom := "平台邮箱"
	if email == "" {
		email = a.Cfg.Site.ACMEEmail
	} else {
		emailFrom = "用户账户邮箱"
	}
	// 24h 限流（读最近签发时间：以 cert_path 非空 + activated_ts 近似判断成本高，改用 dns_checked_ts 不可靠，
	// 因此用 audit 之外的轻量方式：检查证书文件是否已存在且 24h 内更新）
	certDir := filepath.Join(cfgDir, "live", "custom-"+safeDomainFile(domain))
	if fi, err := os.Stat(filepath.Join(certDir, "fullchain.pem")); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		errOut(w, 429, "rate_limited", "该域名 24 小时内已签发过证书：请直接使用现有证书，或稍后再试（Let's Encrypt 有每周签发次数限制）")
		return
	}
	// 调 certbot（webroot 插件；非交互）
	// --config-dir 按用户隔离：certbot 仅在**账户注册时**使用 -m，共享账户会静默忽略邮箱；
	// 且 LE 的「同一标识失败授权次数」限流按账户计，隔离后用户之间不会互相拖累。
	args := []string{
		"certonly", "--webroot", "-w", a.Cfg.Site.WebrootDir,
		"-d", domain,
		"--config-dir", cfgDir,
		"--work-dir", filepath.Join(cfgDir, "work"),
		"--logs-dir", filepath.Join(cfgDir, "logs"),
		"--non-interactive", "--agree-tos",
		"-m", email,
		"--cert-name", "custom-" + safeDomainFile(domain),
		"--deploy-hook", "systemctl reload nginx",
	}
	// 先把 ACME 账户建好、并把联系邮箱登记为**用户账户邮箱**（失败不阻断签发）。
	// 用独立的预算，避免占用下面签发阶段的 120s。
	a.ensureACMEAccount(r.Context(), cfgDir, email)
	ctx, cancel := contextWithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "certbot", args...).CombinedOutput()
	if err != nil {
		msg := certbotErrBrief(string(out))
		log.Printf("[domain] certbot 签发失败 domain=%s email=%s err=%v", domain, emailFrom, err)
		_, _ = a.DB.Exec("UPDATE custom_domains SET last_error=? WHERE id=?", "证书签发失败："+msg, id)
		errOut(w, 502, "cert_issue_failed", "证书签发失败："+msg)
		return
	}
	liveDir := filepath.Join(cfgDir, "live", "custom-"+safeDomainFile(domain))
	certPath := filepath.Join(liveDir, "fullchain.pem")
	keyPath := filepath.Join(liveDir, "privkey.pem")
	// 读到期时间（用于前端提醒）
	var exp int64
	if b, err := os.ReadFile(certPath); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
				exp = c.NotAfter.Unix()
			}
		}
	}
	_, _ = a.DB.Exec(
		`UPDATE custom_domains SET cert_type='letsencrypt', cert_path=?, key_path=?, cert_expires_ts=?, last_error='' WHERE id=?`,
		certPath, keyPath, exp, id)
	a.auditAppend("domain_cert_issue", actx.UserID, "domain="+domain, clientIP(r))
	activated, actErr := a.activateDomain(id)
	jsonOut(w, 200, map[string]any{
		"ok": true, "activated": activated, "activate_error": actErr, "cert_expires_ts": exp,
		"message": func() string {
			if activated {
				return "证书已自动签发并生效，域名可用了：" + domain
			}
			return "证书已签发，但激活失败：" + actErr
		}(),
	})
}

// myDomainsDelete DELETE /v1/my/domains/{id} → 删除域名（清 nginx 配置 + 可选吊销证书）
func (a *App) myDomainsDelete(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	domain, ok := a.ownDomain(actx.UserID, id)
	if !ok {
		errOut(w, 404, "not_found", "域名不存在或不属于你")
		return
	}
	// 清 nginx 配置（失败不阻断删除——残留配置无证书时 nginx -t 会失败，故尽力而为并留痕）
	if err := a.removeNginxVhost(domain); err != nil {
		log.Printf("[domain] 清理 nginx 配置失败 domain=%s: %v", domain, err)
		a.logError("domain_nginx_cleanup_failed", domain, actx.UserID, 0, err.Error())
	}
	if _, err := a.DB.Exec("DELETE FROM custom_domains WHERE id=? AND user_id=?", id, actx.UserID); err != nil {
		errOut(w, 500, "internal_error", "删除失败")
		return
	}
	a.auditAppend("domain_delete", actx.UserID, "domain="+domain, clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "message": "域名已删除（nginx 配置已清理）"})
}

// ---- 内部辅助 ----

// ownDomain 校验域名归属，返回域名
func (a *App) ownDomain(uid, id int64) (string, bool) {
	var domain string
	var owner int64
	if err := a.DB.QueryRow("SELECT user_id, domain FROM custom_domains WHERE id=?", id).Scan(&owner, &domain); err != nil {
		return "", false
	}
	if owner != uid {
		return "", false
	}
	return domain, true
}

// safeDomainFile 域名 → 安全文件名（正则已保证字符集，此处再做一次防御）
func safeDomainFile(domain string) string {
	var b strings.Builder
	for _, c := range domain {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// acmeAccountsDir 每用户独立 ACME 账户目录的父目录（certbot --config-dir 用）。
//
// 为什么必须按用户隔离：
//  1. certbot 只在**账户注册时**使用 -m 邮箱，共享账户会静默忽略 -m → 用户邮箱根本不生效；
//  2. Let's Encrypt 的「同一标识失败授权次数（5 次/小时）」限流按**账户**计，
//     独立账户可避免一个用户的失败把其他用户一起拖进限流。
const acmeAccountsDir = "/etc/letsencrypt-accounts"

// acmeConfigDir 该用户的 certbot 配置目录（账户密钥/证书/续期配置都在这里）
func (a *App) acmeConfigDir(userID int64) string {
	return filepath.Join(acmeAccountsDir, "u"+strconv.FormatInt(userID, 10))
}

var acmeEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]{2,}$`)

// userACMEEmail 取用户账户绑定的邮箱作为 ACME 联系邮箱；
// 为空/非法返回 ""（调用方兜底平台邮箱——无效邮箱会让 certbot 拒绝注册）。
func (a *App) userACMEEmail(userID int64) string {
	var email string
	if err := a.DB.QueryRow("SELECT COALESCE(email,'') FROM users WHERE id=?", userID).Scan(&email); err != nil {
		return ""
	}
	email = strings.TrimSpace(email)
	if len(email) > 190 || !acmeEmailRe.MatchString(email) {
		return ""
	}
	return email
}

// ensureACMEAccount 确保该用户的 ACME 账户已建立，并把联系邮箱登记为**用户账户邮箱**。
//
// 为什么要单独做（certbot 2.1.0 实测）：
//   - `certonly -m X` 与 `register -m X` 都不会把邮箱写进账户（注册后 contact 仍为空）；
//   - `update_account -m X` 会明确回报 "Your e-mail address was updated to X"，是唯一可靠路径。
//
// 失败一律不阻断签发（邮箱只影响 LE 的到期提醒，与证书能否签发无关）。
func (a *App) ensureACMEAccount(ctx context.Context, cfgDir, email string) {
	base := []string{
		"--config-dir", cfgDir,
		"--work-dir", filepath.Join(cfgDir, "work"),
		"--logs-dir", filepath.Join(cfgDir, "logs"),
		"--non-interactive",
	}
	// ① 账户不存在则先注册（已存在时 certbot 会报错，忽略即可）
	if _, err := os.Stat(filepath.Join(cfgDir, "accounts")); err != nil {
		args := append(append([]string{"register"}, base...), "--agree-tos", "-m", email)
		cctx, cancel := contextWithTimeout(ctx, 60*time.Second)
		out, rerr := exec.CommandContext(cctx, "certbot", args...).CombinedOutput()
		cancel()
		if rerr != nil {
			log.Printf("[domain] ACME 账户注册未成功（继续尝试签发）: %s", certbotErrBrief(string(out)))
		}
	}
	// ② 显式登记/更新联系邮箱（幂等）
	args := append(append([]string{"update_account"}, base...), "-m", email)
	cctx, cancel := contextWithTimeout(ctx, 60*time.Second)
	out, err := exec.CommandContext(cctx, "certbot", args...).CombinedOutput()
	cancel()
	if err != nil {
		log.Printf("[domain] ACME 联系邮箱登记失败（不影响签发）: %s", certbotErrBrief(string(out)))
	}
}

// certbotErrBrief 从 certbot 输出里提取**真正的原因**。
//
// 不能截取尾部：certbot 2.1.0 + josepy 1.13.0 有 bug——ACME 错误对象（如 rateLimited）
// 继承自 josepy 的 JSONObject，Python 3.11 在 contextlib 退出时回写 exc.__traceback__，
// 触发 josepy 的 __setattr__（禁止赋值）→ 真实错误被 "AttributeError: can't set attribute"
// 覆盖，且落在输出**尾部**。截尾 400 字符恰好只留下无意义的 traceback（生产实锤：
// 用户看到的永远是 AttributeError，真实原因是 LE 限流）。
// 故改为正向按关键字抽取：ACME 协议错误 > 常见可读原因 > AttributeError 专述 > 兜底。
func certbotErrBrief(out string) string {
	lines := strings.Split(out, "\n")
	brief := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > 300 {
			s = s[:300] + "…"
		}
		return s
	}
	// ① ACME 协议错误（信息量最大）
	for _, l := range lines {
		if i := strings.Index(l, "urn:ietf:params:acme:error:"); i >= 0 {
			return brief(l[i:])
		}
	}
	// ② 常见可读原因
	for _, kw := range []string{
		"DNS problem", "NXDOMAIN", "Some challenges have failed",
		"too many failed authorizations", "Too many certificates",
		"Timeout during connect", "Connection refused", "Invalid response",
		"unauthorized", "is not a FQDN",
	} {
		for _, l := range lines {
			if strings.Contains(l, kw) {
				return brief(l)
			}
		}
	}
	// ③ certbot/josepy 的掩盖型报错：给出人话指引
	if strings.Contains(out, "AttributeError: can't set attribute") {
		return "certbot 内部错误（该版本 certbot/josepy 会把真实原因掩盖为 AttributeError）。" +
			"常见原因：域名未解析到本机、80 端口不可达、或触发了 Let's Encrypt 限流；" +
			"请确认解析已生效后重试，或改用「上传自己的证书」"
	}
	// ④ 兜底：最后一条非空且不像 traceback 的行
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" || strings.HasPrefix(s, "File \"") || strings.HasPrefix(s, "^") ||
			s == "Traceback (most recent call last):" {
			continue
		}
		return brief(s)
	}
	return "certbot 未返回可读错误，请稍后重试或改用「上传自己的证书」"
}

// activateDomain 激活域名：证书齐备 → 生成 nginx vhost → nginx -t → reload → 置 active。
// 返回 (是否激活成功, 错误说明)。
func (a *App) activateDomain(id int64) (bool, string) {
	var domain, certPath, keyPath, status string
	if err := a.DB.QueryRow(
		"SELECT domain, COALESCE(cert_path,''), COALESCE(key_path,''), status FROM custom_domains WHERE id=?",
		id).Scan(&domain, &certPath, &keyPath, &status); err != nil {
		return false, "域名记录不存在"
	}
	if certPath == "" || keyPath == "" {
		return false, "" // 尚无证书：不算错误，等用户上传/签发
	}
	if _, err := os.Stat(certPath); err != nil {
		return false, "证书文件不存在：" + certPath
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false, "私钥文件不存在：" + keyPath
	}
	if err := a.writeNginxVhost(domain, certPath, keyPath); err != nil {
		_, _ = a.DB.Exec("UPDATE custom_domains SET status='failed', last_error=? WHERE id=?", err.Error(), id)
		return false, err.Error()
	}
	now := time.Now().Unix()
	_, _ = a.DB.Exec("UPDATE custom_domains SET status='active', activated_ts=?, last_error='' WHERE id=?", now, id)
	return true, ""
}

// writeNginxVhost 生成用户域名 vhost 并热加载。
// 安全：域名已经过正则白名单；写入前 nginx -t 校验，失败回滚删除。
func (a *App) writeNginxVhost(domain, certPath, keyPath string) error {
	dir := a.Cfg.Site.NginxDir
	if dir == "" {
		return fmt.Errorf("未配置 nginx 目录（site.nginx_dir），无法生成 vhost")
	}
	webroot := a.Cfg.Site.WebrootDir
	if webroot == "" {
		webroot = "/var/www/acme"
	}
	conf := fmt.Sprintf(`# AQUA 自定义域名（自动生成，请勿手改）：%s
server {
    listen 80;
    server_name %s;
    location /.well-known/acme-challenge/ { root %s; }
    location / { return 301 https://$host$request_uri; }
}
server {
    listen 443 ssl;
    server_name %s;
    ssl_certificate     %s;
    ssl_certificate_key %s;
    ssl_protocols TLSv1.2 TLSv1.3;

    client_max_body_size 20m;

    location /v1/ {
        proxy_pass http://127.0.0.1:8790;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 600s;
    }
    location / { return 404; }
}
`, domain, domain, webroot, domain, certPath, keyPath)

	path := filepath.Join(dir, "custom-"+safeDomainFile(domain)+".conf")
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		return fmt.Errorf("写入 nginx 配置失败：%v", err)
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		_ = os.Remove(path) // 回滚
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return fmt.Errorf("nginx 配置校验失败（已回滚）：%s", msg)
	}
	if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		return fmt.Errorf("nginx reload 失败：%s", msg)
	}
	log.Printf("[domain] vhost 已生效 domain=%s", domain)
	return nil
}

// removeNginxVhost 删除用户域名 vhost 并热加载
func (a *App) removeNginxVhost(domain string) error {
	dir := a.Cfg.Site.NginxDir
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, "custom-"+safeDomainFile(domain)+".conf")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		return fmt.Errorf("nginx 校验失败：%s", msg)
	}
	_, _ = exec.Command("systemctl", "reload", "nginx").CombinedOutput()
	log.Printf("[domain] vhost 已移除 domain=%s", domain)
	return nil
}

// adminDomainsGet GET /v1/admin/domains → 全部自定义域名（含证书状态/异常）
func (a *App) adminDomainsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rows, err := a.DB.Query(
		`SELECT d.id, d.user_id, COALESCE(u.username,''), d.domain, d.status,
		        COALESCE(d.cert_type,''), COALESCE(d.cert_expires_ts,0), COALESCE(d.last_error,''),
		        COALESCE(d.created_ts,0), COALESCE(d.activated_ts,0)
		 FROM custom_domains d LEFT JOIN users u ON u.id=d.user_id
		 ORDER BY d.created_ts DESC LIMIT 500`)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	now := time.Now().Unix()
	for rows.Next() {
		var id, uid, certExp, created, activated int64
		var username, domain, status, certType, lastErr string
		if rows.Scan(&id, &uid, &username, &domain, &status, &certType, &certExp, &lastErr, &created, &activated) != nil {
			continue
		}
		it := map[string]any{
			"id": id, "user_id": uid, "username": username, "domain": domain, "status": status,
			"cert_type": certType, "cert_expires_ts": certExp, "last_error": lastErr,
			"created_ts": created, "activated_ts": activated,
		}
		if certExp > 0 {
			it["cert_days_left"] = (certExp - now) / 86400
		}
		items = append(items, it)
	}
	jsonOut(w, 200, map[string]any{"domains": items, "enabled": a.domainFeatureOn()})
}

// adminDomainDisable POST /v1/admin/domains/{id}/disable → 管理端停用（违规域名下线）
func (a *App) adminDomainDisable(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var domain string
	if err := a.DB.QueryRow("SELECT domain FROM custom_domains WHERE id=?", id).Scan(&domain); err != nil {
		errAdmin(w, 404, "not_found", "域名不存在")
		return
	}
	if err := a.removeNginxVhost(domain); err != nil {
		log.Printf("[domain] 停用时清理 nginx 失败 domain=%s: %v", domain, err)
	}
	if _, err := a.DB.Exec("UPDATE custom_domains SET status='disabled', last_error='已被管理员停用' WHERE id=?", id); err != nil {
		errAdmin(w, 500, "internal_error", "停用失败")
		return
	}
	a.auditAppend("domain_disable", 0, "domain="+domain, clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "message": "域名已停用（nginx 配置已清理）"})
}

// ensureDomainSchema 建表兜底（与 db.InitTables 幂等；便于测试与旧库补表）
func (a *App) ensureDomainSchema() {
	_, _ = a.DB.Exec(`CREATE TABLE IF NOT EXISTS custom_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		domain TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL DEFAULT 'pending',
		verify_method TEXT NOT NULL DEFAULT '',
		dns_checked_ts INTEGER NOT NULL DEFAULT 0,
		cert_type TEXT NOT NULL DEFAULT '',
		cert_path TEXT NOT NULL DEFAULT '',
		key_path TEXT NOT NULL DEFAULT '',
		cert_expires_ts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_ts INTEGER NOT NULL DEFAULT 0,
		activated_ts INTEGER NOT NULL DEFAULT 0)`)
}
