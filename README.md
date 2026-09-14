# AQUA api —— 开源 AI API 网关（Go 版）

> 全量重构自 Rust 版（行为规格见仓库 `docs/Go重构总体方案.md`）。
> 设计红线：**代码只含机制，不含事实**——上游地址/密钥/成本率/售价/站点信息全部来自配置。

## 快速开始

```bash
cp config.example.toml config.toml   # 填入你自己的上游与价格
go build -o bin/aqua ./cmd/aqua
./bin/aqua --config config.toml
```

零配置启动 = 空壳站（无上游可调，`/v1/models` 返回空列表）。

## 配置（四层，优先级从低到高）

```
代码内置默认值 < config.toml < 环境变量(AQUA_*) < 命令行 flag
```

价格组/活动价时间窗等热调参走管理后台（DB 存储，改完即时生效，无需重启）。

## 目录

```
cmd/aqua/           入口
internal/config/    四层配置加载与合并
internal/db/        SQLite（纯 Go 驱动，零 CGO）+ 幂等迁移
internal/billing/   计费内核：三段价/预扣退差/保本线/面值台账（全程 int64）
internal/upstream/  上游抽象：Line + 密钥池 + SSE 流式管道
internal/httpapi/   路由与 handler（net/http 标准库，零框架）
```

## 部署

```bash
GOOS=linux GOARCH=amd64 go build -o bin/aqua-linux ./cmd/aqua
```
