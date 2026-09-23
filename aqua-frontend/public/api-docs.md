# AQUA api 接口文档（Markdown 版）

> OpenAI 兼容的多模型聚合 API 网关。本文件为纯文本版本，便于 AI 助手与自动化工具直接读取。
> Base URL：`https://api.ltzy.top/v1` ｜ 站点：https://acu.ltzy.top
> 模型清单与实时价格以 `GET /v1/models` 与 [模型广场](https://acu.ltzy.top/models) 为准。

## 1. 鉴权

所有接口使用站点控制台创建的 API 密钥：

```
Authorization: Bearer sk-你的密钥
```

密钥按**计费分组**划分：免费分组密钥只能调用免费模型（`acu/` 前缀），按次/按量分组密钥调用对应收费模型。分组不匹配会返回 `404 model_not_found`。

## 2. 快速开始

```bash
curl https://api.ltzy.top/v1/chat/completions \
  -H "Authorization: Bearer sk-你的密钥" \
  -H "Content-Type: application/json" \
  -d '{
        "model": "acu/deepseek-v4-flash",
        "messages": [{"role": "user", "content": "你好"}]
      }'
```

```python
from openai import OpenAI

client = OpenAI(api_key="sk-你的密钥", base_url="https://api.ltzy.top/v1")
resp = client.chat.completions.create(
    model="acu/deepseek-v4-flash",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
```

```js
import OpenAI from 'openai'

const client = new OpenAI({ apiKey: 'sk-你的密钥', baseURL: 'https://api.ltzy.top/v1' })
const resp = await client.chat.completions.create({
  model: 'acu/deepseek-v4-flash',
  messages: [{ role: 'user', content: '你好' }],
})
console.log(resp.choices[0].message.content)
```

## 3. 端点一览

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/chat/completions` | 对话补全（支持 `stream` 流式） |
| GET | `/v1/models` | 模型清单（含计费方式与价格） |
| GET | `/v1/models/status` | 模型实时指标（首字延迟 / 吞吐） |
| GET | `/v1/models/{id}` | 模型详情 |
| POST | `/v1/embeddings` | 文本向量化 |
| POST | `/v1/rerank` | 重排 |
| POST | `/v1/moderations` | 内容风险检测 |
| POST | `/v1/images/generations` | 文生图（按张计费） |
| POST | `/v1/audio/speech` | 语音合成 |
| POST | `/v1/audio/transcriptions` | 语音转写（multipart） |
| POST | `/v1/videos/generations` | 视频生成 |
| POST | `/v1/ip_location` | IP 归属地查询 |
| POST | `/v1/tools/hash` | 哈希计算（MD5/SHA1/SHA256/SHA512） |
| POST | `/v1/tools/password` | 强密码生成 |
| POST | `/v1/tools/color` | 颜色格式互转 |
| POST | `/v1/tools/token-count` | token 估算 |
| POST | `/v1/tools/text-stats` | 文本统计 |
| POST | `/v1/tools/shorten` | 短链生成 |
| POST | `/v1/tools/webhook` | Webhook 请求收集 |
| GET | `/v1/meta` | 站点元信息（公开） |
| GET | `/v1/status` | 线路与模型状态（公开） |

## 4. 请求参数（`/v1/chat/completions`）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `model` | string | 必填 | 完整模型 ID，含线路前缀（如 `acu/deepseek-v4-flash`） |
| `messages` | array | 必填 | 对话消息列表，每项含 `role`（system / user / assistant）与 `content` |
| `stream` | boolean | false | 开启 SSE 流式输出 |
| `max_tokens` | integer | — | 本次最大输出 token 数，受模型上下文窗口约束 |
| `temperature` | number | 1.0 | 采样温度，越高越发散 |
| `top_p` | number | 1.0 | 核采样阈值 |
| `stop` | string / array | null | 停止序列 |
| `frequency_penalty` | number | 0 | 重复惩罚 |
| `presence_penalty` | number | 0 | 新话题鼓励 |
| `seed` | integer | null | 随机种子（部分模型支持） |

响应体与 OpenAI 官方一致：`choices[].message.content` 为正文，`usage` 给出 token 用量。

## 5. 流式输出（SSE）

请求体带 `"stream": true`，服务端返回 `text/event-stream`：

```
data: {"choices":[{"delta":{"content":"你"}}]}

data: {"choices":[{"delta":{"content":"好"}}]}

data: [DONE]
```

## 6. 计费口径

| 线路 | 计费方式 |
|---|---|
| `acu/` | 免费：注册即用，不扣个人余额 |
| `aqua/`（按次） | 固定单价/次，与输入输出长度无关 |
| `aqua/`（按量） | 输入 / 缓存命中 / 输出三段分别计价，单位元每百万 tokens |

- **先付后用**：收费模型请求前按上限预扣，完成后按实际用量多退少补
- **失败不计费**：失败或已退款的请求不扣费、不占密钥配额
- **整数记账**：金额以整数微元记账（1 元 = 1,000,000 微元）
- 想降低单次预扣金额，可在请求中调小 `max_tokens`

## 7. 错误码

| HTTP | code | 处理建议 |
|---|---|---|
| 401 | `invalid_api_key` | 检查密钥是否完整、是否带 `Bearer ` 前缀 |
| 401 | `key_expired` | 密钥已过期，在控制台续期或新建 |
| 403 | `key_quota_exceeded` | 该密钥限额用尽，调整限额或换密钥 |
| 403 | `free_grp_restricted` | 免费分组密钥不能调用收费模型 |
| 429 | `insufficient_quota` | 账户余额不足（先付后用），充值后恢复 |
| 429 | `rate_limited` | 请求过快或连续失败触发退避，加入指数退避 |
| 400 | `vision_not_supported` | 该模型为纯文本模型，不接受图片输入 |
| 404 | `model_not_found` | 模型 ID 不在当前密钥分组可用范围 |
| 502 | `upstream_error` | 上游瞬时异常，稍后重试 |
| 503 | `model_channel_unavailable` | 该模型专属通道暂不可用，其他模型不受影响 |
| 503 | `line_exhausted` | 该线路额度耗尽，换线路或模型 |

## 8. 相关资源

- [模型广场](https://acu.ltzy.top/models)：全部可用模型与实时价格
- [OpenAPI 规范](https://acu.ltzy.top/openapi.json)
- [llms.txt](https://acu.ltzy.top/llms.txt) / [llms-full.txt](https://acu.ltzy.top/llms-full.txt)：站点事实摘要（供 AI 读取）
- [服务状态](https://acu.ltzy.top/status)
- [社区](https://acu.ltzy.top/community)：QQ 群反馈
