// 系统在线更新：gitee 发行版源自动抓取。
//   GET  /v1/admin/update/check —— 拉取发行版列表并与当前版本比对
//   POST /v1/admin/update/apply —— 校验二次密码 → 下载附件 → ELF 校验 →
//                                  备份当前二进制 → 原子替换 → systemd 自重启
// 安全：均为管理会话保护；apply 高危操作强制二次密码 + 审计留痕。
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const giteeAPIBase = "https://gitee.com/api/v5"

// giteeRelease gitee 发行版（v5 API 字段子集）
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

// updateCfg 归一化更新配置（默认值兜底）
func (a *App) updateCfg() (repo, token, asset, service string, enabled bool) {
	u := &a.Cfg.Update
	repo = strings.Trim(u.Repo, "/ ")
	token = u.Token
	asset = u.AssetName
	if asset == "" {
		asset = "aqua-gateway-go-linux-amd64"
	}
	service = u.Service
	if service == "" {
		service = "aqua-gateway-go"
	}
	enabled = u.Enabled && repo != ""
	return
}

// —— GET /v1/admin/update/check ——
func (a *App) handleAdminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	repo, token, asset, service, enabled := a.updateCfg()
	if repo == "" {
		adminJSON(w, map[string]any{
			"update_enabled": false,
			"current_version": gatewayVersion,
			"message":         "未配置更新源（config [update] repo）",
			"releases":        []map[string]any{},
		})
		return
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=10", giteeAPIBase, repo)
	if token != "" {
		url += "&access_token=" + token
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		errAdmin(w, 502, "upstream_error", "更新源连接失败："+shortErr(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		errAdmin(w, 502, "upstream_error", fmt.Sprintf("更新源返回 %d（检查 repo/token 配置）", resp.StatusCode))
		return
	}
	var rels []giteeRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		errAdmin(w, 502, "upstream_error", "更新源响应解析失败")
		return
	}
	releases := []map[string]any{}
	for _, rel := range rels {
		if rel.Draft {
			continue
		}
		item := map[string]any{
			"tag": rel.TagName, "name": rel.Name, "notes": rel.Body,
			"published_at": rel.PublishedAt,
			"is_current":   versionMatch(rel.TagName, gatewayVersion),
			"asset_name":   "", "asset_size": int64(0), "downloadable": false,
		}
		for _, as := range rel.Assets {
			if as.Name == asset {
				item["asset_name"] = as.Name
				item["asset_size"] = as.Size
				item["downloadable"] = true
				break
			}
		}
		releases = append(releases, item)
	}
	adminJSON(w, map[string]any{
		"update_enabled": enabled, "repo": repo, "service": service,
		"current_version": gatewayVersion, "asset_name": asset,
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

// —— POST /v1/admin/update/apply ——（高危：二次密码）
func (a *App) handleAdminUpdateApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	repo, token, asset, service, enabled := a.updateCfg()
	if !enabled || repo == "" {
		errAdmin(w, 403, "invalid_request", "在线更新未启用（config [update] enabled=true 且 repo 非空）")
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

	// 1. 查发行版拿下载地址
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", giteeAPIBase, repo, tag)
	if token != "" {
		url += "?access_token=" + token
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		errAdmin(w, 502, "upstream_error", "更新源连接失败："+shortErr(err))
		return
	}
	var rel giteeRelease
	err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel)
	resp.Body.Close()
	if err != nil {
		errAdmin(w, 502, "upstream_error", "发行版信息解析失败")
		return
	}
	dl := ""
	for _, as := range rel.Assets {
		if as.Name == asset {
			dl = as.BrowserDownloadURL
			break
		}
	}
	if dl == "" {
		errAdmin(w, 404, "not_found", fmt.Sprintf("发行版 %s 未找到附件 %s", tag, asset))
		return
	}

	// 2. 下载到临时文件（200MB 上限）
	tmpPath := exePath() + ".update.tmp"
	if err := downloadAsset(dl, token, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		errAdmin(w, 502, "upstream_error", "下载失败："+shortErr(err))
		return
	}
	// 3. 校验：ELF 头（linux 可执行）
	if err := validateELF(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		errAdmin(w, 502, "upstream_error", "附件校验失败："+err.Error())
		return
	}
	// 4. 备份 + 原子替换
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
	if err := os.Chmod(exe, 0o755); err != nil {
		_ = os.Chmod(exe, 0o755)
	}
	a.auditAppend("update_apply", 0, gatewayVersion+" → "+tag, clientIP(r))
	// 5. 异步自重启（detach 会话，避免随本进程被杀）
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
