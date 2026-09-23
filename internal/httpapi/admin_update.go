// 系统在线更新：双镜像源（gitee 主仓库 + github 自动同步镜像）。
//
// 发版工作流（站长开发机）：
//	1. 构建 linux/amd64 二进制，提交进仓库 assets/ 目录，打 tag 后 push；
//	   gitee 会自动同步代码与 tag 到 github 镜像仓库。
//	2. （可选）gitee 网页端创建 Release 附更新日志。
//
// 服务器侧抓取策略（海外访问 gitee 常被 CDN 拦截，故 github 为主）：
//	check：github tags（匿名畅通）列版本 + gitee releases（更新日志元数据），双源合并；
//	apply：按 tag 依次尝试 gitee release 附件 → github raw → gitee raw。
//
// 安全：均为管理会话保护；apply 高危操作强制二次密码 + 审计留痕；
//		 下载校验 ELF 头 + 最小体积，替换前自动备份，失败自动回滚。
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	giteeAPIBase  = "https://gitee.com/api/v5"
	githubAPIBase = "https://api.github.com"
)

// updateHTTPClient 更新请求客户端（配置 proxy 时走代理出口）
func (a *App) updateHTTPClient(timeout time.Duration) *http.Client {
	t := &http.Transport{}
	if p := strings.TrimSpace(a.Cfg.Update.Proxy); p != "" {
		if pu, err := url.Parse(p); err == nil && pu.Scheme != "" {
			t.Proxy = http.ProxyURL(pu)
		}
	} else {
		t.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: timeout, Transport: t}
}

// updateAPIBase gitee v5 API 地址（可指向国内跳板反代）
func (a *App) updateAPIBase() string {
	if b := strings.TrimRight(strings.TrimSpace(a.Cfg.Update.APIBase), "/"); b != "" {
		return b
	}
	return giteeAPIBase
}

// isGiteeURL URL 主机是否为 gitee.com 官方域名（含子域）。
// token/access_token 只允许发给 gitee，避免私有仓库令牌泄露给镜像源或跳板反代。
func isGiteeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Host == "gitee.com" || strings.HasSuffix(u.Host, ".gitee.com")
}

// updateCfg 归一化更新配置（默认值兜底）
func (a *App) updateCfg() (repo, mirrorRepo, token, asset, assetDir, service string, enabled bool) {
	u := &a.Cfg.Update
	repo = strings.Trim(u.Repo, "/ ")
	mirrorRepo = strings.Trim(u.MirrorRepo, "/ ")
	token = u.Token
	asset = u.AssetName
	if asset == "" {
		asset = "aqua-gateway-go-linux-amd64"
	}
	assetDir = strings.Trim(u.AssetDir, "/ ")
	if assetDir == "" {
		assetDir = "assets"
	}
	service = u.Service
	if service == "" {
		service = "aqua-gateway-go"
	}
	enabled = u.Enabled && (repo != "" || mirrorRepo != "")
	return
}

// giteeRelease gitee 发行版（v5 API 字段子集，与 GitHub API 字段名兼容）
type giteeRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	Assets      []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type ghTag struct {
	Name string `json:"name"`
}

// relItem 检查结果里的一条版本
type relItem struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
	AssetSize   int64  `json:"asset_size"`
	Source      string `json:"source"` // gitee-release | github-raw | gitee-raw
	IsCurrent   bool   `json:"is_current"`
	Downloadable bool  `json:"downloadable"`
}

// fetchGiteeReleases gitee releases 元数据（失败返回 nil）
func (a *App) fetchGiteeReleases(repo, token string) []giteeRelease {
	if repo == "" {
		return nil
	}
	u := fmt.Sprintf("%s/repos/%s/releases?per_page=10", a.updateAPIBase(), repo)
	if token != "" && isGiteeURL(u) {
		u += "&access_token=" + token
	}
	resp, err := a.updateHTTPClient(4 * time.Second).Get(u) // 海外被拦时 TLS 挂死，4 秒足以断定
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var rels []giteeRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		return nil
	}
	return rels
}

// fetchGithubTags github 镜像 tags（匿名；海外畅通；失败返回 nil）
func (a *App) fetchGithubTags(mirrorRepo string) []string {
	if mirrorRepo == "" {
		return nil
	}
	u := fmt.Sprintf("%s/repos/%s/tags?per_page=20", githubAPIBase, mirrorRepo)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := a.updateHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var tags []ghTag
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&tags); err != nil {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}

// —— GET /v1/admin/update/check ——
func (a *App) handleAdminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	repo, mirrorRepo, token, asset, _, service, enabled := a.updateCfg()
	if !enabled {
		adminJSON(w, map[string]any{
			"update_enabled": false, "current_version": gatewayVersion,
			"message":  "未配置更新源（config [update] repo / mirror_repo）",
			"releases": []map[string]any{},
		})
		return
	}

	// 双源并发拉取
	type giteeRes struct {
		rels []giteeRelease
		ok   bool
	}
	type ghRes struct {
		tags []string
		ok   bool
	}
	gc := make(chan giteeRes, 1)
	hc := make(chan ghRes, 1)
	go func() { rels := a.fetchGiteeReleases(repo, token); gc <- giteeRes{rels, rels != nil} }()
	go func() { tags := a.fetchGithubTags(mirrorRepo); hc <- ghRes{tags, tags != nil} }()
	// 双源并发，但整体最多等 5 秒：到点未返回的源按不可达（ok=false）处理，
	// 避免被拦源（TLS 挂死）拖住整个检查接口导致前端长时间白屏
	var gr giteeRes
	var hr ghRes
	gotG, gotH := false, false
	deadline := time.After(5 * time.Second)
	for !(gotG && gotH) {
		select {
		case gr = <-gc:
			gotG = true
		case hr = <-hc:
			gotH = true
		case <-deadline:
			gotG, gotH = true, true // 超时的源保持零值：rels/tags 为 nil、ok=false
		}
	}

	// 合并：tag → 条目（gitee release 元数据优先，github tags 补充版本）
	byTag := map[string]*relItem{}
	var order []string
	for _, rel := range gr.rels {
		if rel.Draft || rel.TagName == "" {
			continue
		}
		item := &relItem{
			Tag: rel.TagName, Name: rel.Name, Notes: rel.Body,
			PublishedAt: rel.PublishedAt, Source: "gitee-release", Downloadable: false,
		}
		for _, as := range rel.Assets {
			if as.Name == asset {
				item.AssetSize = as.Size
				item.Downloadable = true
				break
			}
		}
		byTag[rel.TagName] = item
		order = append(order, rel.TagName)
	}
	for _, tag := range hr.tags {
		if _, ok := byTag[tag]; ok {
			continue
		}
		byTag[tag] = &relItem{
			Tag: tag, Source: "github-raw",
			Downloadable: repo != "" || mirrorRepo != "", // raw 按 tag 下载（apply 时实际校验）
		}
		order = append(order, tag)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] > order[j] }) // 版本倒序（日期式 tag 字典序=时间序）
	releases := make([]map[string]any, 0, len(order))
	for _, tag := range order {
		it := byTag[tag]
		it.IsCurrent = versionMatch(tag, gatewayVersion)
		releases = append(releases, map[string]any{
			"tag": it.Tag, "name": it.Name, "notes": it.Notes,
			"published_at": it.PublishedAt, "asset_size": it.AssetSize,
			"source": it.Source, "is_current": it.IsCurrent, "downloadable": it.Downloadable,
		})
	}

	sources := map[string]any{
		"gitee":  map[string]any{"repo": repo, "ok": gr.ok},
		"github": map[string]any{"repo": mirrorRepo, "ok": hr.ok},
	}
	adminJSON(w, map[string]any{
		"update_enabled": enabled, "current_version": gatewayVersion,
		"asset_name": asset, "service": service,
		"repo": repo, "mirror_repo": mirrorRepo, "sources": sources,
		"releases": releases,
	})
}

// versionMatch 版本比对：tag 与当前版本宽松匹配（忽略 v 前缀）
func versionMatch(tag, current string) bool {
	t := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	c := strings.TrimPrefix(strings.TrimSpace(current), "v")
	return t != "" && t == c
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// assetRawURLs 按 tag 生成候选下载地址（顺序：github raw → gitee raw）
func (a *App) assetRawURLs(mirrorRepo, repo, tag, assetDir, asset string) []string {
	var out []string
	path := assetDir + "/" + asset
	if mirrorRepo != "" {
		out = append(out, fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", mirrorRepo, tag, path))
	}
	if repo != "" {
		out = append(out, fmt.Sprintf("https://gitee.com/%s/raw/%s/%s", repo, tag, path))
	}
	return out
}

// updateApplyMu apply 全程互斥：重试/并发触发时保护"备份→替换→清理"关键段，
// 防止第二请求在第一请求替换中途 Rename 同一二进制或误删其回滚副本
var updateApplyMu sync.Mutex

// —— POST /v1/admin/update/apply ——（高危：二次密码）
func (a *App) handleAdminUpdateApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	updateApplyMu.Lock()
	defer updateApplyMu.Unlock()
	repo, mirrorRepo, token, asset, assetDir, service, enabled := a.updateCfg()
	if !enabled {
		errAdmin(w, 403, "invalid_request", "在线更新未启用（config [update] enabled=true 且配置仓库）")
		return
	}
	// Windows 上 systemd 重启链路不存在（detachStart 静默不重启），
	// 置换文件只会白白备份/替换却无法生效——任何实质动作前直接拒绝
	if runtime.GOOS != "linux" {
		errAdmin(w, 400, "bad_request", "在线更新仅支持 Linux 生产环境")
		return
	}
	var req struct {
		Tag             string `json:"tag"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if !a.confirmOK(w, r, "update_apply", req.ConfirmPassword) {
		return
	}
	tag := strings.TrimSpace(req.Tag)
	if tag == "" {
		errAdmin(w, 400, "bad_request", "缺少目标版本 tag")
		return
	}
	if versionMatch(tag, gatewayVersion) {
		errAdmin(w, 400, "bad_request", "当前已是该版本")
		return
	}

	// 候选下载地址：gitee release 附件 → github raw（tag） → gitee raw（tag）
	var candidates []string
	if rels := a.fetchGiteeReleases(repo, token); rels != nil {
		for _, rel := range rels {
			if rel.TagName != tag {
				continue
			}
			for _, as := range rel.Assets {
				if as.Name == asset {
					candidates = append(candidates, a.rewriteAssetURL(as.BrowserDownloadURL))
				}
			}
		}
	}
	candidates = append(candidates, a.assetRawURLs(mirrorRepo, repo, tag, assetDir, asset)...)

	// 逐源下载（第一个成功即用）
	tmpPath := exePath() + ".update.tmp"
	var lastErr error
	downloaded := false
	for _, dl := range candidates {
		if err := a.downloadAsset(dl, token, tmpPath); err != nil {
			lastErr = err
			_ = os.Remove(tmpPath)
			continue
		}
		downloaded = true
		break
	}
	if !downloaded {
		errAdmin(w, 502, "upstream_error", "全部更新源下载失败（最后一源："+shortErr(lastErr)+"）")
		return
	}
	if err := validateELF(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		errAdmin(w, 502, "upstream_error", "附件校验失败："+err.Error())
		return
	}

	// 备份 + 原子替换。备份名带秒级时间戳：重试/并发（互斥锁兜底）下每次备份
	// 都唯一，绝不覆盖/删除既有的回滚副本（旧实现按版本号命名且先 Remove，
	// 一次误触发即可销毁唯一备份）。
	exe := exePath()
	bakPath := exe + ".bak." + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(exe, bakPath); err != nil {
		_ = os.Remove(tmpPath)
		errAdmin(w, 500, "internal_error", "备份当前二进制失败："+shortErr(err))
		return
	}
	pruneOldBackups(exe)
	if err := os.Rename(tmpPath, exe); err != nil {
		_ = os.Rename(bakPath, exe) // 回滚
		errAdmin(w, 500, "internal_error", "替换二进制失败（已回滚）："+shortErr(err))
		return
	}
	_ = os.Chmod(exe, 0o755)
	a.auditAppend("update_apply", 0, gatewayVersion+" → "+tag, clientIP(r))
	// 异步自重启（detach 会话，避免随本进程被杀）
	go func() {
		time.Sleep(800 * time.Millisecond)
		cmd := exec.Command("systemctl", "restart", service)
		_ = detachStart(cmd)
	}()
	adminJSON(w, map[string]any{
		"ok": true, "tag": tag,
		"from_version": gatewayVersion, "backup": filepath.Base(bakPath),
		"message": "新版本已就位，服务正在重启（约 3 秒后恢复）",
	})
}

// rewriteAssetURL 附件下载地址跟随跳板：api_base 非官方时，
// 将 gitee.com 主机替换为 api_base 的主机（路径保留），跳板需反代
// /api/v5/* 与 /*/releases/download/* 两类路径。
func (a *App) rewriteAssetURL(dl string) string {
	base := a.updateAPIBase()
	if base == giteeAPIBase {
		return dl
	}
	i := strings.Index(dl, "gitee.com/")
	if i < 0 {
		return dl
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return dl
	}
	return u.Scheme + "://" + u.Host + "/" + dl[i+len("gitee.com/"):]
}

// exePath 当前二进制绝对路径
func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return "/proc/self/exe"
	}
	if rp, err2 := filepath.EvalSymlinks(p); err2 == nil {
		return rp
	}
	return p
}

// pruneOldBackups 按前缀 <exe>.bak. 清理历史备份：按修改时间保留最近 3 个，
// 更旧的删除。清理失败仅记日志不阻断更新（备份堆积无害，删除备份失败更不该卡住主流程）。
func pruneOldBackups(exe string) {
	dir := filepath.Dir(exe)
	prefix := filepath.Base(exe) + ".bak."
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("[update] 清理旧备份：列目录失败: %v", err)
		return
	}
	var baks []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			baks = append(baks, e)
		}
	}
	if len(baks) <= 3 {
		return
	}
	sort.Slice(baks, func(i, j int) bool {
		ti, ei := baks[i].Info()
		tj, ej := baks[j].Info()
		if ei != nil || ej != nil {
			return ej != nil // 取不到 ModTime 的条目排后面
		}
		return ti.ModTime().After(tj.ModTime())
	})
	for _, e := range baks[3:] {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			log.Printf("[update] 清理旧备份 %s 失败: %v", e.Name(), err)
		}
	}
}

// maxAssetBytes 附件体积上限 200MB（防呆上限，正常 linux/amd64 网关二进制远小于此）
const maxAssetBytes = 200 << 20

// downloadAsset 流式下载附件（App 方法：复用 updateHTTPClient 的代理出口配置）
func (a *App) downloadAsset(url, token, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Authorization 是 gitee 私有仓凭据，只发给 gitee 官方域名
	if token != "" && isGiteeURL(url) {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := a.updateHTTPClient(300 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxAssetBytes {
		return fmt.Errorf("附件超过 200MB 上限（Content-Length %d）", resp.ContentLength)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	// LimitReader 多读 1 字节：恰好 200MB 的附件读完 err==nil 时，
	// n>上限 才能识别"被截断/超限"，避免把截断文件误判为下载成功
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return err
	}
	if n > maxAssetBytes {
		return fmt.Errorf("附件超过 200MB 上限")
	}
	if n < 1<<20 {
		return fmt.Errorf("附件过小（%d 字节），疑似损坏", n)
	}
	// 声明长度可得时校验落盘大小一致（完整性兜底；上游元数据无可解析 sha256）
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return fmt.Errorf("附件不完整（已下载 %d / 声明 %d 字节）", n, resp.ContentLength)
	}
	return nil
}

// validateELF linux 可执行文件头校验
func validateELF(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := f.Read(head); err != nil {
		return err
	}
	if head[0] != 0x7f || head[1] != 'E' || head[2] != 'L' || head[3] != 'F' {
		return fmt.Errorf("非 ELF 可执行文件")
	}
	return nil
}
