// 入口：加载配置 → 初始化数据库 → 价格播种 → 启动 HTTP。
// 零配置启动 = 空壳站（无上游可调，/v1/models 返回空列表）。
// 优雅退出：SIGTERM/SIGINT → 停止接入新请求 → 等待在途请求（含流式长响应）完成
// （上限 180s，systemd TimeoutStopSec 需 ≥200s），之后进程退出由 systemd 秒级拉起——
// 热更部署（systemctl restart）时在途请求零丢失，掉线窗口压缩到毫秒级。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/db"
	"acu-aqua/gateway/internal/httpapi"
	"acu-aqua/gateway/internal/seeding"
)

// shutdownTimeout 优雅退出上限：流式响应预算最长 600s，取 180s 兜底
// （热更窗口内仍在生成的超长流会被提前收尾——绝大多数请求远短于此；
// systemd TimeoutStopSec 需 ≥200s，否则 systemd 会提前 SIGKILL 中断排水）。
const shutdownTimeout = 180 * time.Second

func main() {
	cfgPath := flag.String("config", "config.toml", "配置文件路径（不存在则纯 ENV/默认值启动）")
	listen := flag.String("listen", "", "覆盖监听地址（最高优先级）")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置加载失败: %v\n", err)
		os.Exit(1)
	}
	if *listen != "" {
		cfg.Server.Listen = *listen
	}

	d, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("数据库打开失败: %v", err)
	}
	defer d.Close()
	if err := db.InitTables(d); err != nil {
		log.Fatalf("建表迁移失败: %v", err)
	}
	if err := seeding.SeedAll(d, cfg); err != nil {
		log.Fatalf("价格播种失败: %v", err)
	}

	app := httpapi.New(cfg, &db.DBx{DB: d})
	log.Printf("[%s] 监听 %s · 已配置 %d 条上游线", cfg.Site.Name, cfg.Server.Listen, len(cfg.Lines))
	if len(cfg.Lines) == 0 {
		log.Printf("提示：未配置任何上游线（[[lines]]），当前为空壳站，仅提供基础接口")
	}

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 30 * time.Second, // 慢连接攻击防护（Slowloris）
	}

	// 优雅退出：SIGTERM（systemd restart）/ SIGINT（手动 Ctrl+C）触发
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务退出: %v", err)
		}
	case sig := <-quit:
		log.Printf("收到 %v 信号：优雅关闭中——停止接入新请求，等待在途请求完成（上限 %v）…", sig, shutdownTimeout)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("优雅关闭超时/异常（在途请求已尽力等待）: %v", err)
		} else {
			log.Printf("全部在途请求已完成，进程退出")
		}
	}
}
