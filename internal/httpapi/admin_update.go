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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
	if token != "" {
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

// —— POST /v1/admin/update/apply ——（高危：二次密码）
func (a *App) handleAdminUpdateApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	repo, mirrorRepo, token, asset, assetDir, service, enabled := a.updateCfg()
	if !enabled {
		errAdmin(w, 403, "invalid_request", "在线更新未启用（config [update] enabled=true 且配置仓库）")
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
		if err := downloadAsset(dl, token, tmpPath); err != nil {
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

	// 备份 + 原子替换
	exe := exePath()
	bakPath := exe + ".bak." + gatewayVersion
	_ = os.Remove(bakPath)
	if err := os.Rename(exe, bakPath); err != nil {
		_ = os.Remove(tmpPath)
		errAdmin(w, 500, "internal_error", "备份当前二进制失败："+shortErr(err))
		return
	}
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

// downloadAsset 流式下载附件
func downloadAsset(url, token, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return err
	}
	if n < 1<<20 {
		return fmt.Errorf("附件过小（%d 字节），疑似损坏", n)
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
