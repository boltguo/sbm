package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/boltguo/sbm/internal/auth"
	"github.com/boltguo/sbm/internal/core"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/releasecheck"
	"github.com/boltguo/sbm/internal/server"
	"github.com/boltguo/sbm/internal/store"
	"github.com/boltguo/sbm/internal/systeminfo"
	"github.com/boltguo/sbm/internal/traffic"
	"github.com/boltguo/sbm/internal/webembed"
	"golang.org/x/crypto/bcrypt"
)

var version = "dev"

const (
	defaultConfig     = "/etc/sbm/config.json"
	defaultState      = "/var/lib/sbm/state.json"
	defaultCoreConfig = "/etc/sing-box/config.json"
	defaultSingBox    = "/usr/local/bin/sing-box"
	defaultCert       = "/etc/sbm/cert/fullchain.pem"
	defaultKey        = "/etc/sbm/cert/key.pem"
)

type paths struct{ config, state, coreConfig, singBox, cert, key string }

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	if len(os.Args) < 2 {
		runServe(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "init":
		runInit(os.Args[2:])
	case "config":
		runConfig(os.Args[2:])
	case "admin":
		runAdmin(os.Args[2:])
	case "version", "--version", "-version":
		fmt.Println(version)
	default:
		fatal("未知命令；可用命令：serve、init、config apply、admin reset、version")
	}
}

func pathFlags(set *flag.FlagSet) *paths {
	p := &paths{}
	set.StringVar(&p.config, "config", defaultConfig, "业务配置路径")
	set.StringVar(&p.state, "state", defaultState, "流量状态路径")
	set.StringVar(&p.coreConfig, "core-config", defaultCoreConfig, "sing-box 配置路径")
	set.StringVar(&p.singBox, "sing-box", defaultSingBox, "sing-box 二进制路径")
	set.StringVar(&p.cert, "cert", defaultCert, "TLS 证书路径")
	set.StringVar(&p.key, "key", defaultKey, "TLS 私钥路径")
	return p
}

func runInit(args []string) {
	set := flag.NewFlagSet("init", flag.ExitOnError)
	p := pathFlags(set)
	domain := set.String("domain", "", "已解析到服务器的域名")
	panelPort := set.Int("panel-port", model.DefaultConfig().PanelPort, "面板监听端口")
	nodeName := set.String("node-name", "MyNode", "节点基础名称")
	passwordFile := set.String("admin-password-file", "", "管理员初始密码文件")
	_ = set.Parse(args)
	if *domain == "" || *passwordFile == "" {
		fatal("init 必须提供 --domain 和 --admin-password-file")
	}
	if _, err := os.Stat(p.config); err == nil {
		fatal("业务配置已存在，拒绝覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		fatal("检查业务配置失败")
	}
	passwordBytes, err := os.ReadFile(*passwordFile)
	if err != nil {
		fatal("读取管理员密码失败")
	}
	password := strings.TrimSpace(string(passwordBytes))
	if len(password) < 12 || len(password) > 128 {
		fatal("管理员密码长度必须为 12 到 128")
	}
	baseName := strings.TrimSpace(*nodeName)
	if len([]rune(baseName)) == 0 || len([]rune(baseName)) > 74 {
		fatal("节点基础名称长度必须为 1 到 74 个字符")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fatal("密码哈希失败")
	}
	sessionSecret, err := protocol.RandomToken(32)
	must(err)
	clashSecret, err := protocol.RandomToken(32)
	must(err)
	subToken, err := protocol.RandomToken(32)
	must(err)
	cfg := model.DefaultConfig()
	cfg.Domain = strings.ToLower(strings.TrimSpace(*domain))
	cfg.PanelPort = *panelPort
	cfg.AdminPasswordHash = string(hash)
	cfg.SessionSecret = sessionSecret
	cfg.ClashAPISecret = clashSecret
	cfg.SubscriptionToken = subToken
	factory := protocol.Factory{Keys: protocol.SingBoxKeyGenerator{Binary: p.singBox}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vless, err := factory.New(ctx, protocol.TypeVLESSReality, baseName+"-VLESS", 443)
	must(err)
	hy2, err := factory.New(ctx, protocol.TypeHysteria2, baseName+"-HY2", 443)
	must(err)
	cfg.Inbounds = []model.Inbound{vless, hy2}
	if err := protocol.DefaultRegistry().ValidateConfig(cfg); err != nil {
		fatal(err.Error())
	}
	if err := store.NewJSONFile[model.Config](p.config).SaveWithoutBackup(cfg); err != nil {
		fatal("保存配置失败")
	}
	state := model.DefaultState(time.Now())
	if err := store.NewJSONFile[model.State](p.state).SaveWithoutBackup(state); err != nil {
		fatal("保存状态失败")
	}
	fmt.Println("初始化完成")
}

func runConfig(args []string) {
	if len(args) < 1 || args[0] != "apply" {
		fatal("用法：sbm-panel config apply [flags]")
	}
	set := flag.NewFlagSet("config apply", flag.ExitOnError)
	p := pathFlags(set)
	noStart := set.Bool("no-start", false, "仅生成和校验，不启动服务")
	_ = set.Parse(args[1:])
	cfgStore, manager := loadCore(p)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.Apply(ctx, cfgStore.Get(), *noStart); err != nil {
		fatal(err.Error())
	}
	fmt.Println("sing-box 配置已生成并通过校验")
}

func runAdmin(args []string) {
	if len(args) < 1 || args[0] != "reset" {
		fatal("用法：sbm-panel admin reset [--config path]")
	}
	set := flag.NewFlagSet("admin reset", flag.ExitOnError)
	configPath := set.String("config", defaultConfig, "业务配置路径")
	_ = set.Parse(args[1:])
	cfgStore, err := store.OpenConfig(*configPath)
	if err != nil {
		fatal("读取配置失败")
	}
	password, err := protocol.RandomToken(24)
	must(err)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	must(err)
	cfg := cfgStore.Get()
	cfg.AdminPasswordHash = string(hash)
	if err := cfgStore.Replace(cfg); err != nil {
		fatal("保存新密码失败")
	}
	fmt.Println(password)
}

func runServe(args []string) {
	set := flag.NewFlagSet("serve", flag.ExitOnError)
	p := pathFlags(set)
	plainHTTP := set.Bool("http", false, "开发模式：使用 HTTP")
	procRoot := set.String("proc-root", "/proc", "系统状态 /proc 路径")
	osRelease := set.String("os-release", "/etc/os-release", "系统版本文件路径")
	diskPath := set.String("disk-path", "/", "磁盘统计路径")
	_ = set.Parse(args)
	cfgStore, manager := loadCore(p)
	cfg := cfgStore.Get()
	tracker, err := traffic.Open(p.state, cfgStore, manager, time.Now)
	if err != nil {
		fatal("读取流量状态失败")
	}
	assets, err := fs.Sub(webembed.Assets, "dist")
	if err != nil {
		fatal("读取前端资源失败")
	}
	clashClient := traffic.ClashClient{URL: "http://127.0.0.1:9090/connections", Secret: cfg.ClashAPISecret}
	systemCollector := systeminfo.New()
	systemCollector.ProcRoot, systemCollector.OSReleasePath, systemCollector.DiskPath = *procRoot, *osRelease, *diskPath
	app := &server.Server{
		Config: cfgStore, Traffic: tracker, Core: manager, Registry: protocol.DefaultRegistry(),
		Factory: protocol.Factory{Keys: protocol.SingBoxKeyGenerator{Binary: p.singBox}}, Clash: clashClient,
		System: systemCollector, Assets: assets, Limiter: auth.NewLimiter(),
		Sessions: auth.Sessions{Secret: []byte(cfg.SessionSecret), Lifetime: 24 * time.Hour}, PanelVersion: version,
		Releases: releasecheck.NewGitHub("boltguo/sbm"), TrafficAudit: traffic.NewNetworkAudit(*procRoot),
	}
	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", cfg.PanelPort), Handler: app.Handler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Cross into the new period first. A machine that was off across the reset
	// date would otherwise enforce the old period's quota, stop the core, and
	// only clear it when the scheduler next ticks — a pointless outage.
	if err := tracker.CheckScheduledReset(ctx); err != nil {
		log.Printf("启动时检查流量重置失败：%v", err)
	}
	if err := tracker.ReconcileQuota(ctx); err != nil {
		log.Printf("启动时校正流量限额失败：%v", err)
	}
	go tracker.Run(ctx, clashClient)
	errCh := make(chan error, 1)
	go func() {
		if *plainHTTP {
			errCh <- httpServer.ListenAndServe()
		} else {
			errCh <- httpServer.ListenAndServeTLS(p.cert, p.key)
		}
	}()
	log.Printf("sbm-panel %s 已启动，监听端口 %d", version, cfg.PanelPort)
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			fatal("面板服务异常退出")
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tracker.Persist(); err != nil {
		log.Printf("关闭时保存流量状态失败：%v", err)
	}
	_ = httpServer.Shutdown(shutdownCtx)
}

func loadCore(p *paths) (*store.ConfigStore, *core.Manager) {
	cfgStore, err := store.OpenConfig(p.config)
	if err != nil {
		fatal("读取业务配置失败（主文件与备份均不可用）")
	}
	registry := protocol.DefaultRegistry()
	if err := registry.ValidateConfig(cfgStore.Get()); err != nil {
		fatal("业务配置校验失败：" + err.Error())
	}
	manager := &core.Manager{Binary: p.singBox, ConfigPath: p.coreConfig, Service: "sing-box.service", Commands: core.ExecCommander{}, Renderer: core.Renderer{Registry: registry, BuildContext: protocol.BuildContext{CertificatePath: p.cert, KeyPath: p.key}}}
	return cfgStore, manager
}

func fatal(message string) { fmt.Fprintln(os.Stderr, "错误："+message); os.Exit(1) }
func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}
