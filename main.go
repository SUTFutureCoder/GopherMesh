package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	mesh "github.com/SUTFutureCoder/gophermesh/sdk"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to the GopherMesh config file")
	dashHost := flag.String("dashboard-host", "", "Override dashboard host (e.g. 0.0.0.0 for LAN access)")
	dashPort := flag.String("dashboard-port", "", "Override dashboard port")
	noDashboard := flag.Bool("no-dashboard", false, "Do not auto-open the dashboard browser UI")
	noProtocol := flag.Bool("noprotocol", false, "Disable gophermesh:// protocol registration and launch handling")
	protocolURL := flag.String("protocol-url", "", "Internal custom protocol launch url")
	flag.Parse()

	if *noProtocol && *protocolURL != "" {
		log.Fatal("protocol launch is disabled by -noprotocol")
	}

	if !*noProtocol {
		if err := mesh.RegisterLaunchProtocol(); err != nil {
			log.Printf("register gophermesh protocol skipped: %v", err)
		}
	}

	effectiveConfigPath := *configPath
	var protocolReq mesh.LaunchProtocolRequest
	if *protocolURL != "" {
		var err error
		protocolReq, err = mesh.ParseLaunchProtocol(*protocolURL)
		if err != nil {
			log.Fatal(err)
		}
		effectiveConfigPath, err = mesh.ResolveLaunchConfigPath(*configPath, protocolReq.ConfigPath)
		if err != nil {
			log.Fatal(err)
		}
	}

	// 1. 加载配置
	cfg, err := mesh.LoadConfig(effectiveConfigPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cfg.ConfigPath = effectiveConfigPath
	if *dashHost != "" {
		cfg.DashboardHost = *dashHost
	}
	if *dashPort != "" {
		cfg.DashboardPort = *dashPort
	}

	if *protocolURL != "" {
		if err := mesh.ValidateLaunchPort(cfg, protocolReq.Port); err != nil {
			log.Fatal(err)
		}
		if protocolReq.Port != "" {
			if mesh.IsPublicRouteHealthy(protocolReq.Port) {
				log.Printf("gophermesh protocol launch ignored: route %s already ready", protocolReq.Port)
				return
			}
			log.Printf("gophermesh protocol launch accepted for public port %s with config %s", protocolReq.Port, effectiveConfigPath)
		} else {
			if mesh.IsDashboardHealthy(cfg) {
				log.Printf("gophermesh protocol launch ignored: dashboard already ready for config %s", effectiveConfigPath)
				return
			}
			log.Printf("gophermesh protocol launch accepted with config %s", effectiveConfigPath)
		}
	}

	// 2. 初始化核心引擎
	engine, err := mesh.NewEngineWithOptions(cfg, mesh.EngineOptions{
		NoDashboard: *noDashboard,
	})
	if err != nil {
		log.Fatalf("init engine: %v", err)
	}

	// 3. 监听系统级别中断信号（Ctrl+C, SIGTERM）
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("starting mesh engine... current role: [%s]", engine.Role())

	// 4. 阻塞运行，直到收到系统中断信号
	if err := engine.Run(ctx); err != nil {
		log.Printf("mesh engine terminated: %v", err)
	}

	// 5. 触发优雅退出，给予底层Python/C++进程5秒善后时间
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := engine.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("mesh engine terminated error: %v", err)
	}

	log.Printf("mesh engine safe terminated")
}
