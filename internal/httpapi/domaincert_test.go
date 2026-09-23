// 自定义域名证书签发回归（20260920）：certbot 报错的"真实原因"必须能被提取出来。
//
// 背景（生产实锤）：用户点「自动签发」永远看到 "AttributeError: can't set attribute"。
// 真实原因是 Let's Encrypt 限流（too many failed authorizations）——因为
//   ① 80 端口默认 server 是 nginx 自带的欢迎页 → ACME 挑战文件取不到 → 反复失败；
//   ② certbot 2.1.0 + josepy 1.13 会把 ACME 错误覆盖成 AttributeError，且落在输出尾部；
//   ③ 旧代码只截取输出尾部 400 字符，恰好把唯一有用的那行切掉。
// 本测试锁定：无论 certbot 怎么掩盖，我们都要把 ACME 协议错误 / 可读原因提取出来。
package httpapi

import (
	"strings"
	"testing"
)

// 生产真实输出（截取关键片段）：ACME 限流被 AttributeError 掩盖，真实原因在 traceback 中间
const realMaskedOutput = `Saving debug log to /var/log/letsencrypt/letsencrypt.log
Requesting a certificate for api.ai.jic8.cc
An unexpected error occurred:
Traceback (most recent call last):
  File "/usr/lib/python3/dist-packages/certbot/_internal/client.py", line 478, in _get_order_and_authorizations
    orderr = self.acme.new_order(csr_pem)
  File "/usr/lib/python3/dist-packages/acme/client.py", line 575, in _check_response
    raise messages.Error.from_json(jobj)
acme.messages.Error: urn:ietf:params:acme:error:rateLimited :: There were too many requests of a given type :: too many failed authorizations (5) for "api.ai.jic8.cc" in the last 1h0m0s, retry after 2026-09-20 02:16:58 UTC

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/usr/lib/python3/dist-packages/certbot/main.py", line 19, in main
    return internal_main.main(cli_args)
  File "/usr/lib/python3.11/contextlib.py", line 188, in __exit__
    exc.__traceback__ = traceback
  File "/usr/lib/python3/dist-packages/josepy/util.py", line 191, in __setattr__
    raise AttributeError("can't set attribute")
AttributeError: can't set attribute
`

func TestCertbotErrBrief_SurfacesRealReasonBehindAttributeError(t *testing.T) {
	got := certbotErrBrief(realMaskedOutput)
	if !strings.Contains(got, "rateLimited") {
		t.Fatalf("必须提取出真实的 ACME 限流原因，实际得到：%q", got)
	}
	if !strings.Contains(got, "too many failed authorizations") {
		t.Fatalf("应包含可读的失败授权说明，实际得到：%q", got)
	}
	if strings.Contains(got, "AttributeError") {
		t.Fatalf("不应把被掩盖的 AttributeError 当作原因返回：%q", got)
	}
}

func TestCertbotErrBrief_OnlyAttributeError(t *testing.T) {
	out := "An unexpected error occurred:\nTraceback (most recent call last):\n  File \"x\", line 1\nAttributeError: can't set attribute\n"
	got := certbotErrBrief(out)
	if !strings.Contains(got, "certbot 内部错误") {
		t.Fatalf("只有 AttributeError 时应给出人话指引，实际得到：%q", got)
	}
	if !strings.Contains(got, "上传自己的证书") {
		t.Fatalf("应给出兜底方案指引，实际得到：%q", got)
	}
}

func TestCertbotErrBrief_CommonReasons(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"DNS 未解析", "Certbot failed to authenticate\n  Detail: DNS problem: NXDOMAIN looking up A for x.com\n", "NXDOMAIN"},
		{"挑战失败", "Some challenges have failed.\nAsk for help on community.letsencrypt.org\n", "Some challenges have failed"},
		{"证书配额", "Error: urn:ietf:params:acme:error:rateLimited :: Too many certificates already issued for x.com\n", "Too many certificates"},
		{"连接被拒", "Detail: Connection refused\n", "Connection refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := certbotErrBrief(c.out); !strings.Contains(got, c.want) {
				t.Fatalf("want 含 %q，得到 %q", c.want, got)
			}
		})
	}
}

func TestCertbotErrBrief_NoTracebackTailAndLength(t *testing.T) {
	// 纯 traceback（无任何可读原因）→ 不应返回 "File ..."/"^" 这类无意义行
	out := "Traceback (most recent call last):\n  File \"/x/y.py\", line 1, in f\n    foo()\n    ^^^^\n"
	got := certbotErrBrief(out)
	if strings.HasPrefix(got, "File ") || strings.HasPrefix(got, "^") {
		t.Fatalf("不应把 traceback 行当原因：%q", got)
	}
	// 超长行必须截断（避免把整个 traceback 塞进 API 响应与 DB last_error）
	long := "Error: " + strings.Repeat("x", 5000)
	if n := len(certbotErrBrief(long)); n > 320 {
		t.Fatalf("超长输出应截断到 320 字符内，实际 %d", n)
	}
}
