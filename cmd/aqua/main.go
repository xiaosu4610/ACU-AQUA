// 入口：加载配置 → 初始化数据库 → 价格播种 → 启动 HTTP。
// 零配置启动 = 空壳站（无上游可调，/v1/models 返回空列表）。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/db"
	"acu-aqua/gateway/internal/httpapi"
	"acu-aqua/gateway/internal/seeding"
)

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
	if err := http.ListenAndServe(cfg.Server.Listen, app.Routes()); err != nil {
		log.Fatalf("HTTP 服务退出: %v", err)
	}
}
