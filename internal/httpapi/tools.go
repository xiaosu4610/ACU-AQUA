package httpapi

// 工具箱端点群（与 Rust misc.rs 契约等价）：
// 纯本地工具（零上游）+ 短链/Webhook 收集器 + 站内 AI 工具通道 + IP 定位

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
)

// ---------------------------------------------------------------------------
// 共用小件
// ---------------------------------------------------------------------------

func readToolJSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求格式不正确，请检查请求体与 Content-Type")
		return nil, false
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		errOut(w, 400, "bad_request", "请求格式不正确，请检查请求体与 Content-Type")
		return nil, false
	}
	return v, true
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randBase62(n int) string {
	const cs = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	out := make([]byte, n)
	max := big.NewInt(int64(len(cs)))
	for i := range out {
		x, err := rand.Int(rand.Reader, max)
		if err != nil {
			out[i] = cs[time.Now().UnixNano()%int64(len(cs))]
			continue
		}
		out[i] = cs[x.Int64()]
	}
	return string(out)
}

func strOf(v map[string]any, key string) string {
	s, _ := v[key].(string)
	return s
}

func boolOf(v map[string]any, key string, def bool) bool {
	if b, ok := v[key].(bool); ok {
		return b
	}
	return def
}

func intOf(v map[string]any, key string, def int64) int64 {
	if f, ok := v[key].(float64); ok {
		return int64(f)
	}
	return def
}

// ---------------------------------------------------------------------------
// 纯本地工具（零上游消耗）
// ---------------------------------------------------------------------------

// handleToolTextStats POST /v1/tools/text-stats
func (a *App) handleToolTextStats(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	text, has := v["text"]
	if !has {
		errOut(w, 400, "bad_request", "缺少 text 字段：请提供要统计的文本")
		return
	}
	s, _ := text.(string)
	cjk, words, sentences := 0, 0, 0
	runes := []rune(s)
	for _, c := range runes {
		if c >= 0x4E00 && c <= 0x9FFF {
			cjk++
		}
	}
	words = len(strings.Fields(s))
	for _, seg := range strings.FieldsFunc(s, func(c rune) bool {
		return c == '。' || c == '！' || c == '？' || c == '.' || c == '!' || c == '?'
	}) {
		if strings.TrimSpace(seg) != "" {
			sentences++
		}
	}
	lines := strings.Count(s, "\n") + 1
	noSpace := 0
	for _, c := range runes {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			noSpace++
		}
	}
	jsonOut(w, 200, map[string]any{
		"chars": len(runes), "chars_no_space": noSpace, "cjk_chars": cjk,
		"words": words, "lines": lines, "bytes": len(s), "sentences": sentences,
	})
}

// handleToolDice POST /v1/tools/dice
func (a *App) handleToolDice(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	sides := clampInt(intOf(v, "sides", 6), 2, 1000)
	count := clampInt(intOf(v, "count", 1), 1, 100)
	rolls := make([]int64, 0, count)
	total := int64(0)
	for i := int64(0); i < count; i++ {
		x, err := rand.Int(rand.Reader, big.NewInt(int64(sides)))
		if err != nil {
			x = big.NewInt(time.Now().UnixNano() % int64(sides))
		}
		rolls = append(rolls, x.Int64()+1)
		total += rolls[len(rolls)-1]
	}
	jsonOut(w, 200, map[string]any{"sides": sides, "count": count, "rolls": rolls, "total": total})
}

func clampInt(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleToolUUID GET /v1/tools/uuid
func (a *App) handleToolUUID(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"uuid": newUUID()})
}

// handleToolUUIDBulk POST /v1/tools/uuid-bulk
func (a *App) handleToolUUIDBulk(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	count := clampInt(intOf(v, "count", 10), 1, 100)
	uuids := make([]string, 0, count)
	for i := int64(0); i < count; i++ {
		uuids = append(uuids, newUUID())
	}
	jsonOut(w, 200, map[string]any{"count": count, "uuids": uuids})
}

// handleToolTimestampGet GET /v1/tools/timestamp
func (a *App) handleToolTimestampGet(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	jsonOut(w, 200, map[string]any{
		"timestamp":    now.Unix(),
		"timestamp_ms": now.UnixMilli(),
		"iso8601":      now.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

var tsLayouts = []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006/01/02 15:04:05", "2006-01-02"}

// handleToolTimestampPost POST /v1/tools/timestamp
func (a *App) handleToolTimestampPost(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	if _, has := v["timestamp"]; has {
		ts := intOf(v, "timestamp", 0)
		t := time.Unix(ts, 0).In(time.FixedZone("UTC+8", 8*3600))
		jsonOut(w, 200, map[string]any{
			"timestamp":     ts,
			"datetime_utc8": t.Format("2006-01-02 15:04:05"),
		})
		return
	}
	if _, has := v["datetime"]; has {
		ds := strOf(v, "datetime")
		for _, layout := range tsLayouts {
			if t, err := time.ParseInLocation(layout, ds, time.UTC); err == nil {
				jsonOut(w, 200, map[string]any{"datetime": ds, "timestamp": t.Unix()})
				return
			}
		}
		errOut(w, 400, "bad_request", "日期时间格式不正确，支持 YYYY-MM-DD HH:MM:SS 等常见格式")
		return
	}
	errOut(w, 400, "bad_request", "缺少参数：请提供 timestamp（时间戳转日期）或 datetime（日期转时间戳）")
}

// base64Encode / base64Decode 标准编码
func base64Encode(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	return string(b), err
}

// handleToolBase64 POST /v1/tools/base64
func (a *App) handleToolBase64(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	if text := strOf(v, "text"); text != "" || v["text"] != nil {
		jsonOut(w, 200, map[string]any{"encoded": base64Encode(text)})
		return
	}
	if enc := strOf(v, "encoded"); enc != "" || v["encoded"] != nil {
		dec, err := base64Decode(enc)
		if err != nil {
			errOut(w, 400, "bad_request", "Base64 格式不正确，无法解码")
			return
		}
		jsonOut(w, 200, map[string]any{"decoded": dec})
		return
	}
	errOut(w, 400, "bad_request", "缺少参数：请提供 text（编码）或 encoded（解码）")
}

// handleToolSubnet POST /v1/tools/subnet
func (a *App) handleToolSubnet(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	cidr := strOf(v, "cidr")
	if cidr == "" {
		errOut(w, 400, "bad_request", "缺少 cidr 字段：请提供如 \"192.168.1.0/24\"")
		return
	}
	ipS, maskS, found := strings.Cut(cidr, "/")
	if !found {
		errOut(w, 400, "bad_request", "cidr 格式不正确，应为 \"IP/掩码位\"，如 \"192.168.1.0/24\"")
		return
	}
	ip := net.ParseIP(strings.TrimSpace(ipS))
	if ip == nil || ip.To4() == nil {
		errOut(w, 400, "bad_request", "IP 或掩码位格式不正确")
		return
	}
	mask, err := strconv.Atoi(strings.TrimSpace(maskS))
	if err != nil || mask > 32 {
		errOut(w, 400, "bad_request", "掩码位范围为 0-32")
		return
	}
	ipU := binaryBigEndianUint32(ip.To4())
	var maskBits uint32
	if mask > 0 {
		maskBits = ^uint32(0) << (32 - mask)
	}
	network := ipU & maskBits
	broadcast := network | ^maskBits
	total := uint64(1) << (32 - mask)
	hosts := total
	if mask <= 30 {
		hosts = total - 2
	}
	jsonOut(w, 200, map[string]any{
		"cidr": cidr,
		"network": u32ToIPv4(network), "broadcast": u32ToIPv4(broadcast),
		"mask": u32ToIPv4(maskBits), "wildcard": u32ToIPv4(^maskBits),
		"mask_bits": mask, "total_addresses": total, "usable_hosts": hosts,
		"first_host": func() string {
			if mask <= 30 {
				return u32ToIPv4(network + 1)
			}
			return u32ToIPv4(network)
		}(),
		"last_host": func() string {
			if mask <= 30 {
				return u32ToIPv4(broadcast - 1)
			}
			return u32ToIPv4(broadcast)
		}(),
	})
}

func binaryBigEndianUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func u32ToIPv4(v uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", v>>24&255, v>>16&255, v>>8&255, v&255)
}

// handleToolHash POST /v1/tools/hash
func (a *App) handleToolHash(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	text, has := v["text"]
	if !has {
		errOut(w, 400, "bad_request", "缺少 text 字段：请提供要计算哈希的文本")
		return
	}
	s, _ := text.(string)
	algo := strings.ToLower(strOf(v, "algo"))
	var digest string
	switch algo {
	case "md5":
		sum := md5.Sum([]byte(s))
		digest = hex.EncodeToString(sum[:])
	case "sha1":
		sum := sha1.Sum([]byte(s))
		digest = hex.EncodeToString(sum[:])
	case "sha256", "":
		sum := sha256.Sum256([]byte(s))
		digest = hex.EncodeToString(sum[:])
		if algo == "" {
			algo = "sha256"
		}
	case "sha512":
		sum := sha512.Sum512([]byte(s))
		digest = hex.EncodeToString(sum[:])
	default:
		errOut(w, 400, "bad_request", "不支持的算法 "+algo+"：可选 md5 / sha1 / sha256 / sha512")
		return
	}
	jsonOut(w, 200, map[string]any{"algo": algo, "digest": digest})
}

// handleToolPassword POST /v1/tools/password
func (a *App) handleToolPassword(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	length := int(clampInt(intOf(v, "length", 16), 8, 64))
	count := int(clampInt(intOf(v, "count", 5), 1, 20))
	pools := []struct {
		name  string
		chars string
	}{
		{"upper", "ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
		{"lower", "abcdefghijklmnopqrstuvwxyz"},
		{"digits", "0123456789"},
		{"symbols", "!@#$%^&*-_=+?"},
	}
	var all []rune
	var required [][]rune
	for _, p := range pools {
		if !boolOf(v, p.name, p.name != "symbols") {
			continue
		}
		all = append(all, []rune(p.chars)...)
		required = append(required, []rune(p.chars))
	}
	if len(all) == 0 {
		errOut(w, 400, "bad_request", "至少启用一种字符类型（upper / lower / digits / symbols）")
		return
	}
	passwords := make([]string, 0, count)
	for i := 0; i < count; i++ {
		chars := make([]rune, length)
		for j := range chars {
			x, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
			if err != nil {
				x = big.NewInt(0)
			}
			chars[j] = all[x.Int64()]
		}
		// 保证每类至少 1 个：前 n 位强制替换
		for k, pool := range required {
			if k < len(chars) {
				x, err := rand.Int(rand.Reader, big.NewInt(int64(len(pool))))
				if err == nil {
					chars[k] = pool[x.Int64()]
				}
			}
		}
		passwords = append(passwords, string(chars))
	}
	jsonOut(w, 200, map[string]any{"length": length, "count": count, "passwords": passwords})
}

// handleToolJSON POST /v1/tools/json
func (a *App) handleToolJSON(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	text, has := v["text"]
	if !has {
		errOut(w, 400, "bad_request", "缺少 text 字段：请提供要校验的 JSON 文本")
		return
	}
	s, _ := text.(string)
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		jsonOut(w, 200, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	typ := "value"
	switch parsed.(type) {
	case map[string]any:
		typ = "object"
	case []any:
		typ = "array"
	}
	pretty, _ := json.MarshalIndent(parsed, "", "  ")
	mini, _ := json.Marshal(parsed)
	jsonOut(w, 200, map[string]any{"valid": true, "type": typ, "pretty": string(pretty), "minified": string(mini)})
}

// handleToolRegex POST /v1/tools/regex
func (a *App) handleToolRegex(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	pattern, text := strOf(v, "pattern"), strOf(v, "text")
	if pattern == "" && v["pattern"] == nil || text == "" && v["text"] == nil {
		errOut(w, 400, "bad_request", "缺少 pattern / text 字段")
		return
	}
	flags := strOf(v, "flags")
	if flags == "" {
		flags = "g"
	}
	var exp string
	if strings.Contains(flags, "i") {
		exp += "(?i)"
	}
	if strings.Contains(flags, "s") {
		exp += "(?s)"
	}
	exp += pattern
	re, err := regexp.Compile(exp)
	if err != nil {
		errOut(w, 400, "bad_request", "正则表达式无效："+err.Error())
		return
	}
	locs := re.FindAllStringSubmatchIndex(text, 100)
	matches := make([]map[string]any, 0, len(locs))
	for _, loc := range locs {
		groups := make([]string, 0)
		for i := 2; i+1 < len(loc); i += 2 {
			groups = append(groups, text[loc[i]:loc[i+1]])
		}
		matches = append(matches, map[string]any{
			"match": text[loc[0]:loc[1]], "start": loc[0], "end": loc[1], "groups": groups,
		})
	}
	jsonOut(w, 200, map[string]any{"pattern": pattern, "count": len(matches), "matches": matches})
}

// handleToolColor POST /v1/tools/color
func (a *App) handleToolColor(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	input, has := v["input"]
	if !has {
		errOut(w, 400, "bad_request", "缺少 input 字段：请提供颜色值（如 #3b82f6 或 rgb(59,130,246)）")
		return
	}
	s := strings.ToLower(strings.TrimSpace(fmt.Sprint(input)))
	var rr, gg, bb uint32
	if hexS, found := strings.CutPrefix(s, "#"); found {
		switch len(hexS) {
		case 3:
			vals := make([]uint32, 0, 3)
			for _, c := range hexS {
				d, err := strconv.ParseUint(string(c), 16, 8)
				if err != nil {
					errOut(w, 400, "bad_request", "颜色格式不正确")
					return
				}
				vals = append(vals, uint32(d*17))
			}
			rr, gg, bb = vals[0], vals[1], vals[2]
		case 6:
			vals := make([]uint32, 0, 3)
			for i := 0; i < 6; i += 2 {
				d, err := strconv.ParseUint(hexS[i:i+2], 16, 16)
				if err != nil {
					errOut(w, 400, "bad_request", "颜色格式不正确")
					return
				}
				vals = append(vals, uint32(d))
			}
			rr, gg, bb = vals[0], vals[1], vals[2]
		default:
			errOut(w, 400, "bad_request", "颜色格式不正确：支持 #rgb 与 #rrggbb")
			return
		}
	} else {
		nums := make([]uint32, 0, 3)
		body := strings.TrimSuffix(strings.TrimPrefix(s, "rgb("), ")")
		for _, p := range strings.FieldsFunc(body, func(c rune) bool { return c == ',' || c == ' ' }) {
			d, err := strconv.ParseUint(p, 10, 16)
			if err != nil {
				continue
			}
			nums = append(nums, uint32(d))
		}
		if len(nums) != 3 {
			errOut(w, 400, "bad_request", "颜色格式不正确：支持 #rrggbb 与 rgb(r,g,b)")
			return
		}
		rr, gg, bb = nums[0], nums[1], nums[2]
	}
	rf, gf, bf := float64(rr)/255, float64(gg)/255, float64(bb)/255
	max, min := rf, rf
	for _, f := range []float64{gf, bf} {
		if f > max {
			max = f
		}
		if f < min {
			min = f
		}
	}
	l := (max + min) / 2
	var h, s2 float64
	if max-min > 1e-9 {
		d := max - min
		if l > 0.5 {
			s2 = d / (2 - max - min)
		} else {
			s2 = d / (max + min)
		}
		switch {
		case max == rf:
			h = ((gf - bf) / d + map[bool]float64{true: 6, false: 0}[gf >= bf]) * 60
		case max == gf:
			h = ((bf-rf)/d + 2) * 60
		default:
			h = ((rf-gf)/d + 4) * 60
		}
	}
	round1 := func(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
	jsonOut(w, 200, map[string]any{
		"hex": fmt.Sprintf("#%02x%02x%02x", rr, gg, bb),
		"rgb": map[string]uint32{"r": rr, "g": gg, "b": bb},
		"hsl": map[string]float64{"h": round1(h), "s": round1(s2 * 100), "l": round1(l * 100)},
	})
}

// handleToolURLCode POST /v1/tools/url-code
func (a *App) handleToolURLCode(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	text, has := v["text"]
	if !has {
		errOut(w, 400, "bad_request", "缺少 text 字段：请提供要编码/解码的文本")
		return
	}
	s, _ := text.(string)
	mode := strOf(v, "mode")
	if mode == "" {
		mode = "encode"
	}
	var result string
	if mode == "decode" {
		result, _ = url.PathUnescape(s)
	} else {
		var sb strings.Builder
		for _, b := range []byte(s) {
			if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
				sb.WriteByte(b)
			} else {
				fmt.Fprintf(&sb, "%%%02X", b)
			}
		}
		result = sb.String()
	}
	jsonOut(w, 200, map[string]any{"mode": mode, "result": result})
}

// handleToolTokenCount POST /v1/tools/token-count
func (a *App) handleToolTokenCount(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	text, has := v["text"]
	if !has {
		errOut(w, 400, "bad_request", "缺少 text 字段：请提供要估算的文本")
		return
	}
	s, _ := text.(string)
	cjk, other := 0, 0
	for _, c := range s {
		if (c >= 0x4E00 && c <= 0x9FFF) || (c >= 0x3000 && c <= 0x303F) || (c >= 0xFF00 && c <= 0xFFEF) {
			cjk++
		} else {
			other++
		}
	}
	tokens := int(float64(cjk)*0.6+float64(other)/4.0) + 1
	jsonOut(w, 200, map[string]any{
		"tokens": tokens, "chars": len([]rune(s)), "cjk_chars": cjk,
		"note": "近似估算值（不同模型分词器有差异），仅供参考",
	})
}

// ---------------------------------------------------------------------------
// 开发者工具：短链 / Webhook 收集器
// ---------------------------------------------------------------------------

const shortLinkBase = "https://acu.ltzy.top"

// handleToolShorten POST /v1/tools/shorten
func (a *App) handleToolShorten(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	u := strings.TrimSpace(strOf(v, "url"))
	if u == "" {
		errOut(w, 400, "bad_request", "缺少 url 字段：请提供要缩短的网址（http/https）")
		return
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		errOut(w, 400, "bad_request", "url 必须以 http:// 或 https:// 开头")
		return
	}
	if len(u) > 2048 {
		errOut(w, 400, "bad_request", "url 过长（上限 2048 字节）")
		return
	}
	var code string
	for i := 0; i < 5; i++ {
		cand := randBase62(6)
		var one string
		if err := a.DB.QueryRow("SELECT code FROM shortlinks WHERE code = ?", cand).Scan(&one); err != nil {
			code = cand
			break
		}
	}
	if code == "" {
		errOut(w, 500, "internal_error", "短链生成失败，请重试")
		return
	}
	ts := time.Now().Unix()
	if _, err := a.DB.Exec(
		"INSERT INTO shortlinks (code, url, hits, created_ts, last_hit_ts) VALUES (?,?,0,?,?)", code, u, ts, ts); err != nil {
		errOut(w, 500, "internal_error", "短链保存失败")
		return
	}
	jsonOut(w, 200, map[string]any{
		"short": shortLinkBase + "/s/" + code,
		"code":  code, "url": u,
		"retention": "90 天无访问自动清理",
	})
}

// handleShortRedirect GET /s/{code} —— 短链 302
func (a *App) handleShortRedirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("id")
	if i := strings.IndexAny(code, "?#"); i >= 0 {
		code = code[:i]
	}
	valid := code != "" && len(code) <= 32
	for _, c := range code {
		if !strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", c) {
			valid = false
			break
		}
	}
	if !valid {
		errOut(w, 404, "not_found", "短链不存在或已过期")
		return
	}
	var u string
	err := a.DB.QueryRow("SELECT url FROM shortlinks WHERE code = ?", code).Scan(&u)
	if err != nil {
		errOut(w, 404, "not_found", "短链不存在或已过期（90 天无访问自动清理）")
		return
	}
	_, _ = a.DB.Exec("UPDATE shortlinks SET hits = hits + 1, last_hit_ts = ? WHERE code = ?", time.Now().Unix(), code)
	w.Header().Set("Location", u)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(302)
}

// handleToolWebhook POST /v1/tools/webhook
func (a *App) handleToolWebhook(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	action := strOf(v, "action")
	if action == "" {
		action = "create"
	}
	switch action {
	case "create":
		id := strings.ReplaceAll(newUUID(), "-", "")[:10]
		ts := time.Now().Unix()
		if _, err := a.DB.Exec("INSERT OR IGNORE INTO hooks (id, created_ts) VALUES (?,?)", id, ts); err != nil {
			errOut(w, 500, "internal_error", "Webhook 创建失败")
			return
		}
		jsonOut(w, 200, map[string]any{
			"id":    id,
			"url":   "https://api.ltzy.top/hook/" + id,
			"usage": fmt.Sprintf("向 https://api.ltzy.top/hook/%s 发送任意方法的请求即被记录，用 action=list 查看（保留 24 小时）", id),
		})
	case "list":
		id := strings.TrimSpace(strOf(v, "id"))
		if id == "" {
			errOut(w, 400, "bad_request", "缺少 id 字段：请提供 Webhook ID")
			return
		}
		rows, err := a.DB.Query(
			"SELECT method, path, headers, body, ts FROM hook_requests WHERE hook_id = ? ORDER BY ts DESC LIMIT 50", id)
		if err != nil {
			errOut(w, 500, "internal_error", "查询失败")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var method, path, headers string
			var body []byte
			var ts int64
			if err := rows.Scan(&method, &path, &headers, &body, &ts); err == nil {
				hj := map[string]any{}
				_ = json.Unmarshal([]byte(headers), &hj)
				items = append(items, map[string]any{
					"method": method, "path": path, "headers": hj,
					"body": string(body), "ts": ts,
				})
			}
		}
		jsonOut(w, 200, map[string]any{"items": items, "total": len(items)})
	case "clear":
		id := strings.TrimSpace(strOf(v, "id"))
		if id == "" {
			errOut(w, 400, "bad_request", "缺少 id 字段：请提供 Webhook ID")
			return
		}
		res, err := a.DB.Exec("DELETE FROM hook_requests WHERE hook_id = ?", id)
		if err != nil {
			errOut(w, 500, "internal_error", "清理失败")
			return
		}
		n, _ := res.RowsAffected()
		jsonOut(w, 200, map[string]any{"cleared": n})
	default:
		errOut(w, 400, "bad_request", "action 取值只能为 create / list / clear")
	}
}

// handleHookCollect ANY /hook/{id} —— 收集任意方法请求
func (a *App) handleHookCollect(w http.ResponseWriter, r *http.Request) {
	hookID := r.PathValue("id")
	if len(hookID) > 64 || !isAlnum(hookID) {
		errOut(w, 404, "not_found", "Webhook 不存在")
		return
	}
	method := r.Method
	path := r.URL.RequestURI()
	headers := map[string]any{}
	for k, vv := range r.Header {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "content-length" || lk == "connection" || lk == "accept-encoding" {
			continue
		}
		if len(headers) >= 30 {
			break
		}
		val := strings.Join(vv, ", ")
		if len(val) > 500 {
			val = val[:500]
		}
		headers[lk] = val
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	hj, _ := json.Marshal(headers)
	ts := time.Now().Unix()
	var exists string
	if err := a.DB.QueryRow("SELECT id FROM hooks WHERE id = ?", hookID).Scan(&exists); err != nil {
		_, _ = a.DB.Exec("INSERT OR IGNORE INTO hooks (id, created_ts) VALUES (?,?)", hookID, ts)
	}
	_, _ = a.DB.Exec(
		"INSERT INTO hook_requests (hook_id, method, path, headers, body, ts) VALUES (?,?,?,?,?,?)",
		hookID, method, path, string(hj), body, ts)
	jsonOut(w, 200, map[string]any{
		"ok":   true,
		"hint": fmt.Sprintf("请求已记录。在 https://acu.ltzy.top 工具箱查看，或 POST /v1/tools/web {\"action\":\"list\",\"id\":\"%s\"}", hookID),
	})
}

func isAlnum(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 内部免费模型调用（树洞/竞技场/AI 工具共用）
// ---------------------------------------------------------------------------

// freeDispatchJSON 内部非流式调一次免费模型，返回 (HTTP 状态, 响应 JSON, 实际使用模型)；
// 网络失败返回 (0, nil, "")。自动记录健康与 usage 记账，指定模型不可用时走健康回退链。
func (a *App) freeDispatchJSON(model string, messages []map[string]any, temperature float64, maxTokens int64) (int, map[string]any, string) {
	retired := a.retiredUpstreams()
	cands := []string{model}
	if _, _, ok := a.freeResolve(model); !ok {
		// 指定模型不可用：树洞链/通用回退 + 健康候选兜底
		cands = []string{"glm-4-flash", "qwen3-8b", "qwen2-7b-instruct", "gpt-oss-20b"}
	}
	// 追加健康候选兜底（近 6h 成功率≥0.9 的模型按序尝试，网络局部故障时自愈）
	for _, hc := range a.autoCandidates() {
		if hc != model && !containsStr(cands, hc) && len(cands) < 6 {
			cands = append(cands, hc)
		}
	}
	for _, cand := range cands {
		ln, up, ok := a.freeResolve(cand)
		if !ok || retired[up] {
			continue
		}
		payload, _ := json.Marshal(map[string]any{
			"model": cand, "messages": messages, "stream": false,
			"temperature": temperature, "max_tokens": maxTokens,
		})
		start := time.Now()
		dummy, _ := http.NewRequest("POST", "/internal", nil)
		req := &chatReq{Model: cand, Stream: false}
		resp, cancel := a.freeUpstreamChat(dummy, payload, req, ln, up)
		if resp == nil {
			a.recordHealth(cand, false, "network_error", 0, time.Since(start).Milliseconds())
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		cancel()
		lat := time.Since(start).Milliseconds()
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			a.markRetired(up)
			continue
		}
		if resp.StatusCode >= 400 || err != nil {
			a.recordHealth(cand, false, healthErrType(resp.StatusCode), resp.StatusCode, lat)
			continue
		}
		a.recordHealth(cand, true, "", resp.StatusCode, lat)
		rid := a.insertRequest(0, "", "tools", cand, false)
		var j map[string]any
		if err := json.Unmarshal(raw, &j); err != nil {
			if rid != 0 {
				a.failRequest(rid, "bad_response")
			}
			continue
		}
		if rid != 0 {
			a.okFreeRequest(rid, usageFromUpstreamJSON(j), resp.StatusCode)
		}
		return resp.StatusCode, j, cand
	}
	return 0, nil, ""
}

// usageFromUpstreamJSON 从上游非流式 JSON 中提取 usage（无则零值）
func usageFromUpstreamJSON(j map[string]any) billing.Usage {
	raw, _ := json.Marshal(j["usage"])
	var uj usageJSON
	_ = json.Unmarshal(raw, &uj)
	return usageFromJSON(&uj)
}

// askModel 用免费模型执行"系统指令 + 用户输入"，取回纯文本回答
func (a *App) askModel(system, user string, maxTokens int64) (string, bool) {
	code, j, _ := a.freeDispatchJSON("glm-4-flash", []map[string]any{
		{"role": "system", "content": system},
		{"role": "user", "content": user},
	}, 0.3, maxTokens)
	if code == 0 || code >= 400 || j == nil {
		return "", false
	}
	content := jsonPointerStr(j, "choices", "0", "message", "content")
	return strings.TrimSpace(content), content != ""
}

// jsonPointerStr 简易 JSON 路径取值（仅字符串）
func jsonPointerStr(j map[string]any, path ...string) string {
	cur := any(j)
	for _, seg := range path {
		switch c := cur.(type) {
		case map[string]any:
			cur = c[seg]
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx >= len(c) {
				return ""
			}
			cur = c[idx]
		default:
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

// ---------------------------------------------------------------------------
// AI 实用端点（封装免费模型，返回纯结果）
// ---------------------------------------------------------------------------

var langNames = map[string]string{
	"zh": "简体中文", "en": "英语", "ja": "日语", "ko": "韩语", "fr": "法语",
	"de": "德语", "es": "西班牙语", "ru": "俄语", "pt": "葡萄牙语", "it": "意大利语",
	"th": "泰语", "vi": "越南语", "ar": "阿拉伯语", "yue": "粤语", "traditional-chinese": "繁体中文",
}

// handleTranslate POST /v1/tools/translate
func (a *App) handleTranslate(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	text := strings.TrimSpace(strOf(v, "text"))
	if text == "" {
		errOut(w, 400, "bad_request", "缺少 text 字段：请提供要翻译的文本")
		return
	}
	if len(text) > 12000 {
		errOut(w, 400, "bad_request", "文本过长（上限 12000 字节），请分段翻译")
		return
	}
	to := strOf(v, "to")
	if to == "" {
		to = "zh"
	}
	toName := langNames[to]
	if toName == "" {
		toName = to
	}
	from := "请自动识别原文语言"
	if f := strings.TrimSpace(strOf(v, "from")); f != "" {
		from = "原文语言是" + langNames[f]
		if langNames[f] == "" {
			from = "原文语言是" + f
		}
	}
	sys := "你是专业翻译引擎。只输出译文本身，不要解释、不要注释、不要原文。"
	user := from + "。请把下面的文本翻译成" + toName + "，保持语气与格式：\n\n" + text
	out, ok2 := a.askModel(sys, user, 4096)
	if !ok2 {
		errOut(w, 502, "upstream_error", "翻译通道暂时不可用，请稍后重试")
		return
	}
	jsonOut(w, 200, map[string]any{"translated": out, "to": to, "model": "glm-4-flash"})
}

// handleURLSummary POST /v1/tools/url-summary
func (a *App) handleURLSummary(w http.ResponseWriter, r *http.Request) {
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	u := strings.TrimSpace(strOf(v, "url"))
	if u == "" {
		errOut(w, 400, "bad_request", "缺少 url 字段：请提供要总结的网页地址（http/https）")
		return
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		errOut(w, 400, "bad_request", "url 必须以 http:// 或 https:// 开头")
		return
	}
	// SSRF 防护：禁内网/本机地址
	if host := urlHost(u); host == "localhost" || strings.HasSuffix(host, ".local") || host == "0.0.0.0" ||
		isPrivateIP(host) || net.ParseIP(host) != nil {
		errOut(w, 400, "bad_request", "不允许访问内网或本机地址，请提供公网 URL")
		return
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		errOut(w, 502, "upstream_error", "网页访问失败：目标站点不可达、超时或拒绝访问")
		return
	}
	html, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	resp.Body.Close()
	if err != nil {
		errOut(w, 502, "upstream_error", "网页内容读取失败，请稍后重试")
		return
	}
	page := string(html)
	title := ""
	if m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(page); len(m) > 1 {
		title = strings.TrimSpace(m[1])
	}
	text := regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</\1>`).ReplaceAllString(page, " ")
	text = regexp.MustCompile(`(?s)<[^>]+>`).ReplaceAllString(text, " ")
	text = htmlUnescape(text)
	var sb strings.Builder
	for i, f := range strings.Fields(text) {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(f)
	}
	bodyText := sb.String()
	if len([]rune(bodyText)) < 50 {
		errOut(w, 422, "bad_request", "该网页正文内容过少（可能是动态渲染页面），无法生成摘要")
		return
	}
	runeBody := []rune(bodyText)
	if len(runeBody) > 6000 {
		runeBody = runeBody[:6000]
	}
	sys := "你是网页摘要助手。用中文总结用户给出的网页正文，输出 3-5 条要点（每条一行、以 · 开头），不超过 200 字。只输出要点，不要开场白。"
	user := "网页标题：" + title + "\n\n正文：" + string(runeBody)
	summary, ok2 := a.askModel(sys, user, 1024)
	if !ok2 {
		errOut(w, 502, "upstream_error", "摘要生成失败，请稍后重试")
		return
	}
	jsonOut(w, 200, map[string]any{"url": u, "title": title, "summary": summary, "model": "glm-4-flash"})
}

func urlHost(u string) string {
	rest := u
	if i := strings.Index(rest, "//"); i >= 0 {
		rest = rest[i+2:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && !strings.Contains(rest[i:], "]") {
		rest = rest[:i]
	}
	return strings.ToLower(strings.Trim(rest, "[]"))
}

func isPrivateIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

var htmlEntities = strings.NewReplacer(
	"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'")

func htmlUnescape(s string) string { return htmlEntities.Replace(s) }

// ---------------------------------------------------------------------------
// IP 定位（双通道：Gitee AI 主 + ip-api 备）
// ---------------------------------------------------------------------------

func validIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

// handleIPLocation POST /v1/ip_location
func (a *App) handleIPLocation(w http.ResponseWriter, r *http.Request) {
	cfIP := r.Header.Get("CF-Connecting-IP")
	xff := r.Header.Get("X-Forwarded-For")
	if i := strings.Index(xff, ","); i >= 0 {
		xff = strings.TrimSpace(xff[:i])
	}
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	ip := strings.TrimSpace(strOf(v, "ip"))
	if ip == "" {
		ip = cfIP
		if ip == "" {
			ip = xff
		}
	}
	if ip == "" {
		errOut(w, 400, "bad_request", "缺少 ip 字段：请在请求体中提供要查询的 IP（如 \"ip\": \"8.8.8.8\"）")
		return
	}
	if net.ParseIP(ip) == nil {
		errOut(w, 400, "bad_request", "IP 格式不正确：「"+ip+"」不是合法的 IPv4/IPv6 地址，请检查后重试")
		return
	}
	if !validIP(ip) {
		errOut(w, 400, "bad_request", "「"+ip+"」是内网/保留 IP，没有公网归属地信息。请查询公网 IP，或留空 ip 字段自动查询调用方出口 IP")
		return
	}
	isV6 := strings.Contains(ip, ":")

	// 主通道：Gitee AI ip-location
	if !isV6 {
		if base, key := a.giteeProvider(); base != "" && key != "" {
			client := &http.Client{Timeout: 30 * time.Second}
			req, _ := http.NewRequest("GET", strings.TrimSuffix(base, "/")+"/ip_location?ip="+url.QueryEscape(ip), nil)
			req.Header.Set("Authorization", "Bearer "+key)
			if res, err := client.Do(req); err == nil {
				body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
				res.Body.Close()
				if res.StatusCode >= 200 && res.StatusCode < 300 {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(res.StatusCode)
					_, _ = w.Write(body)
					return
				}
			}
		}
	}
	// 备用：ip-api.com
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Get("http://ip-api.com/json/" + url.QueryEscape(ip) + "?lang=zh-CN&fields=status,country,regionName,city,isp,org,as,lat,lon,timezone,query")
	if err != nil {
		if isV6 {
			errOut(w, 502, "upstream_error", "IPv6 查询备用数据源不可达，请稍后重试")
		} else {
			errOut(w, 502, "upstream_error", "IP 查询的主/备数据源均不可达（Gitee AI 与 ip-api 均失败），请稍后重试")
		}
		return
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(body)
}

// giteeProvider 免费线里 id=gitee 的 (base_url, 首密钥)
func (a *App) giteeProvider() (string, string) {
	for i := range a.Cfg.Lines {
		l := &a.Cfg.Lines[i]
		if l.ID == "gitee" && l.BaseURL != "" && len(l.Keys) > 0 {
			return l.BaseURL, l.Keys[0]
		}
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// 站内 AI 工具内部对话端点：POST /v1/tools/chat
// 2026-09-05 起强制登录（会话/用户密钥）；model 归一 deepseek-chat / deepseek-reasoner。
// 上游为免费池 deepseek 系模型链（官方 key 未配置时的等价回退），失败前端自动回退公开池。
// ---------------------------------------------------------------------------

// handleToolsChat POST /v1/tools/chat
func (a *App) handleToolsChat(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录官网账号后使用站内 AI 功能")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败")
		return
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		errOut(w, 400, "bad_request", "请求格式不正确")
		return
	}
	model := strOf(v, "model")
	if strings.Contains(model, "reasoner") {
		model = "deepseek-reasoner"
	} else {
		model = "deepseek-chat"
	}
	v["model"] = model
	// 流式响应无法拿到精确 usage：免费池不计费，无需预扣
	isStream := boolOf(v, "stream", false)

	// 上游模型链：deepseek-chat → 免费池 deepseek 系（r1-distill）→ 通用链
	cands := []string{"deepseek-chat", "deepseek-r1-distill-qwen-14b", "deepseek-r1-distill-qwen-7b"}
	if model == "deepseek-reasoner" {
		cands = []string{"deepseek-reasoner", "deepseek-r1-distill-qwen-14b", "deepseek-r1-distill-qwen-7b"}
	}
	retired := a.retiredUpstreams()
	for _, cand := range cands {
		line, upID, ok := a.freeResolve(cand)
		if !ok || retired[upID] {
			continue
		}
		payload, _ := json.Marshal(v)
		start := time.Now()
		req := &chatReq{Model: cand, Stream: isStream}
		resp, cancel := a.freeUpstreamChat(r, payload, req, line, upID)
		if resp == nil {
			a.recordHealth(cand, false, "network_error", 0, time.Since(start).Milliseconds())
			continue
		}
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			_, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			cancel()
			a.markRetired(upID)
			continue
		}
		if isStream && resp.StatusCode < 400 {
			rid := a.insertRequest(actx.UserID, actx.KeyHash, "tools", cand, true)
			w.Header().Set("X-AQUA-Model", cand)
			w.Header().Set("X-AQUA-Line", line.ID)
			a.serveFreeStreamChat(w, r, resp, rid, cand, time.Now())
			return
		}
		if resp.StatusCode >= 400 {
			_, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			cancel()
			a.recordHealth(cand, false, healthErrType(resp.StatusCode), resp.StatusCode, time.Since(start).Milliseconds())
			continue
		}
		rid := a.insertRequest(actx.UserID, actx.KeyHash, "tools", cand, false)
		w.Header().Set("X-AQUA-Model", cand)
		w.Header().Set("X-AQUA-Line", line.ID)
		a.serveFreeJSONChat(w, resp, rid, cand, time.Now())
		return
	}
	// deepseek 系全部不可用：auto 智能路由兜底（免得工具页一刀切 503）
	autoBody, _ := json.Marshal(map[string]any{
		"model": "auto", "messages": v["messages"], "stream": false,
		"temperature": 0.3,
		"max_tokens":  clampInt(intOf(v, "max_tokens", 1024), 1, 8192),
	})
	a.handleFreeChat(w, r, autoBody, &chatReq{Model: "auto", Stream: false}, actx.UserID, actx.KeyHash)
}
