package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/githubtiger123/loom/platform/internal/authn"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/executor"
	"github.com/githubtiger123/loom/platform/internal/handler"
	inframq "github.com/githubtiger123/loom/platform/internal/infra/mq"
	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	infraredis "github.com/githubtiger123/loom/platform/internal/infra/redis"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/migration"
	"github.com/githubtiger123/loom/platform/internal/route"
	"github.com/githubtiger123/loom/platform/internal/service"
	"github.com/githubtiger123/loom/platform/internal/worker"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	role := flag.String("cmd", "api", "运行角色: api | worker | pipeline-worker | kserve-worker | sync | cron | migrate | bootstrap | set-password")
	passwordUserID := flag.Uint64("user-id", 0, "set-password 的既有本地用户 ID；密码从终端隐藏输入或标准输入读取")
	workerConcurrency := flag.Int("worker-concurrency", 4, "worker 并发数，范围 1–16")
	cfgPath := flag.String("config", "configs/config.yaml", "配置文件路径")
	bootstrapIssuer := flag.String("issuer", "", "初始化管理员的已验证 issuer")
	bootstrapSubject := flag.String("subject", "", "初始化管理员的已验证 subject")
	bootstrapUsername := flag.String("username", "", "初始化管理员实际登录名")
	flag.Parse()

	switch *role {
	case "api", "worker", "pipeline-worker", "kserve-worker", "sync", "cron", "migrate", "bootstrap", "set-password":
	default:
		fmt.Fprintf(os.Stderr, "unknown cmd: %s（可选 api / worker / pipeline-worker / kserve-worker / sync / cron / migrate / bootstrap / set-password）\n", *role)
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败：%v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.Log.SlogLevel(),
	}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 运维命令只连接 MySQL，不依赖 Redis、MQ 或集群，也不自动在启动时运行。
	if *role == "migrate" || *role == "bootstrap" || *role == "set-password" {
		db, err := openDB(ctx, cfg)
		if err != nil {
			logger.Error("数据库连接失败", "err", err)
			os.Exit(1)
		}
		defer inframysql.Close(db)
		if *role == "migrate" {
			err = migration.Apply(ctx, db)
		} else if *role == "set-password" {
			if err := migration.Check(ctx, db); err != nil {
				logger.Error("数据库版本检查失败", "err", err)
				os.Exit(1)
			}
			value, readErr := readLocalPassword()
			if readErr != nil {
				err = readErr
			} else {
				err = authn.ProvisionLocalPassword(ctx, db, cfg.Auth, *passwordUserID, value)
			}
		} else {
			if err := migration.Check(ctx, db); err != nil {
				logger.Error("数据库版本检查失败", "err", err)
				os.Exit(1)
			}
			if cfg.Auth.Enabled && cfg.Auth.Issuer != *bootstrapIssuer {
				err = fmt.Errorf("本地模式 bootstrap issuer 必须与 auth.issuer 一致，不能隐式迁移外部身份")
			} else if cfg.Identity.Issuer != "" && cfg.Identity.Issuer != *bootstrapIssuer {
				err = fmt.Errorf("bootstrap issuer 必须与 identity.issuer 一致")
			} else {
				err = service.New(db, cfg.K8s.Cluster, cfg.K8s.InfrastructureOwner).Bootstrap(ctx, *bootstrapIssuer, *bootstrapSubject, *bootstrapUsername)
			}
		}
		if err != nil {
			logger.Error("初始化命令失败", "command", *role, "err", err)
			os.Exit(1)
		}
		logger.Info("初始化命令完成", "command", *role)
		return
	}

	// 镜像目录是 API/worker 的必需静态配置；其他角色不依赖它。
	var notebookImages *config.NotebookImageCatalog
	if *role == "api" || *role == "worker" {
		notebookImages, err = config.LoadNotebookImages(cfg.Images.Path)
		if err != nil {
			logger.Error("加载 Notebook 镜像目录失败", "err", err)
			os.Exit(1)
		}
	}

	logger.Info("loom 启动",
		"role", *role,
		"config", *cfgPath,
		"db", cfg.DB.SafeDSN(),
		"redis_enabled", cfg.Redis.Enabled,
		"redis_addr", cfg.Redis.Addr,
		"mq_enabled", cfg.MQ.Enabled,
		"userid_header", cfg.Identity.UserIDHeader,
	)

	deps, err := buildDeps(ctx, cfg, logger)
	if err != nil {
		logger.Error("依赖初始化失败", "err", err)
		os.Exit(1)
	}
	defer deps.closeAll(logger)
	if err := migration.Check(ctx, deps.db); err != nil {
		logger.Error("数据库版本检查失败", "err", err)
		os.Exit(1)
	}

	k8sClient, err := buildK8sClient(cfg, logger)
	if err != nil {
		// api 允许降级：没有集群时只有 /healthz 可用，/readyz 返回 503
		logger.Error("k8s 客户端初始化失败，服务降级运行", "err", err)
	}

	switch *role {
	case "api":
		if err := runAPI(ctx, cfg, logger, deps, k8sClient, notebookImages); err != nil {
			logger.Error("api 退出", "err", err)
			os.Exit(1)
		}
	default:
		if *workerConcurrency < 1 || *workerConcurrency > 16 {
			logger.Error("worker-concurrency 必须在 1–16 之间")
			os.Exit(1)
		}
		if *role != "cron" && k8sClient == nil {
			logger.Error("后台执行或观测必须配置 Kubernetes 客户端")
			os.Exit(1)
		}
		runner := &worker.Runner{DB: deps.db, Kube: k8sClient, Service: newBusinessService(service.New(deps.db, cfg.K8s.Cluster, cfg.K8s.InfrastructureOwner), k8sClient), Cluster: cfg.K8s.Cluster, Log: logger, Concurrency: *workerConcurrency}
		runner.NotebookImageCatalog = notebookImages
		runner.Service.NotebookImages = notebookImages
		configurePipeline(runner.Service, cfg.Pipeline)
		if *role == "pipeline-worker" {
			if err := configureLogArchive(runner.Service, cfg.LogArchive); err != nil {
				logger.Error("Pipeline 日志归档配置失败", "err", err)
				os.Exit(1)
			}
		}
		configureKServe(runner.Service, cfg.KServe)
		var runErr error
		switch *role {
		case "worker":
			runErr = runner.Run(ctx)
		case "pipeline-worker":
			runErr = runner.RunPipeline(ctx)
		case "kserve-worker":
			runErr = runner.RunKServe(ctx)
		case "sync":
			runErr = runner.RunObserver(ctx)
		case "cron":
			runErr = runner.RunMaintenance(ctx)
		}
		if runErr != nil {
			logger.Error("后台角色退出", "role", *role, "err", runErr)
			os.Exit(1)
		}
	}
}

// ---------- 依赖 ----------

type namedCheck struct {
	name     string
	fn       func(context.Context) error
	optional bool // 可选依赖故障只标记降级，不阻断就绪
}

type deps struct {
	checks  []namedCheck
	closers []func() error
	db      *gorm.DB
	redis   *redis.Client
}

func (d *deps) closeAll(log *slog.Logger) {
	for i := len(d.closers) - 1; i >= 0; i-- {
		if err := d.closers[i](); err != nil {
			log.Warn("关闭依赖失败", "err", err)
		}
	}
}

func buildDeps(ctx context.Context, cfg *config.Config, log *slog.Logger) (*deps, error) {
	d := &deps{}

	// --- MySQL：必需。连不上就没有"谁有什么权限"的答案，直接退出。---
	db, err := openDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	d.db = db
	d.closers = append(d.closers, func() error { return inframysql.Close(db) })
	d.checks = append(d.checks, namedCheck{name: "mysql", fn: func(ctx context.Context) error {
		return inframysql.Health(ctx, db, cfg.DB.PingTimeout.Std())
	}})
	log.Info("MySQL 已连接", "max_open", cfg.DB.MaxOpen)

	// Redis 始终是可选缓存；认证 Token 与撤销状态持久化在 MySQL。
	if cfg.Redis.Enabled {
		rdb := infraredis.New(infraredis.Config{
			Addr:         cfg.Redis.Addr,
			Password:     cfg.Redis.Password,
			DB:           cfg.Redis.DB,
			DialTimeout:  cfg.Redis.DialTimeout.Std(),
			ReadTimeout:  cfg.Redis.ReadTimeout.Std(),
			WriteTimeout: cfg.Redis.WriteTimeout.Std(),
			PoolSize:     cfg.Redis.PoolSize,
		})
		d.closers = append(d.closers, rdb.Close)
		d.redis = rdb
		check := namedCheck{name: "redis", optional: true, fn: func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		}}
		d.checks = append(d.checks, check)
		pingCtx, cancel := context.WithTimeout(ctx, cfg.Redis.DialTimeout.Std())
		err := check.fn(pingCtx)
		cancel()
		if err != nil {
			log.Warn("Redis 不可用，认证与权限缓存降级为直查数据库", "err", err)
		} else {
			log.Info("Redis 已连接", "addr", cfg.Redis.Addr, "db", cfg.Redis.DB)
		}
	}

	// --- RabbitMQ：默认关闭；显式开启就要求连得上，否则静默丢消息比启动失败更糟。---
	if cfg.MQ.Enabled {
		client, err := inframq.Open(inframq.Config{
			URL:      cfg.MQ.URL,
			Exchange: cfg.MQ.Exchange,
			Prefetch: cfg.MQ.Prefetch,
		})
		if err != nil {
			d.closeAll(log)
			return nil, err
		}
		d.closers = append(d.closers, client.Close)
		d.checks = append(d.checks, namedCheck{name: "rabbitmq", fn: func(context.Context) error {
			return client.Health()
		}})
		log.Info("RabbitMQ 已连接", "exchange", cfg.MQ.Exchange)
	}

	return d, nil
}

func openDB(ctx context.Context, cfg *config.Config) (*gorm.DB, error) {
	return inframysql.Open(ctx, inframysql.Config{DSN: cfg.DB.DSN, MaxOpen: cfg.DB.MaxOpen, MaxIdle: cfg.DB.MaxIdle, ConnMaxLifetime: cfg.DB.ConnMaxLifetime.Std(), ConnMaxIdleTime: cfg.DB.ConnMaxIdleTime.Std(), PingTimeout: cfg.DB.PingTimeout.Std()})
}

func buildK8sClient(cfg *config.Config, logger *slog.Logger) (*k8s.Client, error) {
	var (
		restCfg *rest.Config
		err     error
	)

	if cfg.K8s.Kubeconfig != "" {
		logger.Info("使用 kubeconfig 连接集群", "path", cfg.K8s.Kubeconfig)
		restCfg, err = clientcmd.BuildConfigFromFlags("", cfg.K8s.Kubeconfig)
	} else {
		logger.Info("使用 In-Cluster ServiceAccount 连接集群")
		restCfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("构造 rest.Config: %w", err)
	}

	restCfg.QPS = 50
	restCfg.Burst = 100
	restCfg.Timeout = 10 * time.Second

	return k8s.New(restCfg, k8s.Config{
		IngressPrincipal:     cfg.Istio.IngressPrincipal,
		KFPUIPrincipal:       cfg.Istio.KFPUIPrincipal,
		QuotaName:            cfg.K8s.QuotaName,
		EditorServiceAccount: cfg.K8s.EditorServiceAccount,
		UserIDHeader:         cfg.Identity.UserIDHeader,
		UserIDPrefix:         cfg.Identity.UserIDPrefix,
	})
}

// ---------- HTTP ----------

type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func newAPIHandler(d *deps, k8sClient *k8s.Client, business ...http.Handler) http.Handler {
	mux := http.NewServeMux()

	// 存活探针：只说明进程还在，不依赖外部系统
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 仅必需依赖故障返回 503；可选依赖故障返回 200 并标记 degraded。
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]checkResult, len(d.checks)+1)
		ready := true
		degraded := false

		for _, c := range d.checks {
			cctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			err := c.fn(cctx)
			cancel()
			if err != nil {
				degraded = true
				if !c.optional {
					ready = false
				}
				results[c.name] = checkResult{Status: "fail", Error: err.Error()}
				continue
			}
			results[c.name] = checkResult{Status: "ok"}
		}

		switch {
		case k8sClient == nil:
			ready = false
			results["kubernetes"] = checkResult{Status: "fail", Error: "k8s 客户端未初始化"}
		default:
			results["kubernetes"] = checkResult{Status: "ok"}
		}

		body := map[string]any{
			"status": map[bool]string{true: "ok", false: "degraded"}[ready && !degraded],
			"checks": results,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(body)
	})

	if len(business) > 0 {
		mux.Handle("/api/", business[0])
		mux.Handle("/verify", business[0])
		mux.Handle("/verify/", business[0])
	}
	return mux
}

func runAPI(ctx context.Context, cfg *config.Config, logger *slog.Logger, d *deps, k8sClient *k8s.Client, notebookImages *config.NotebookImageCatalog) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	var authentication *authn.Manager
	if cfg.Auth.Enabled {
		var err error
		authentication, err = authn.New(d.db, d.redis, cfg.Auth)
		if err != nil {
			return err
		}
	}
	businessService := newBusinessService(service.New(d.db, cfg.K8s.Cluster, cfg.K8s.InfrastructureOwner), k8sClient)
	businessService.NotebookImages = notebookImages
	configurePipeline(businessService, cfg.Pipeline)
	configureKServe(businessService, cfg.KServe)
	if err := configureLogArchive(businessService, cfg.LogArchive); err != nil {
		return err
	}
	if err := configureArtifactStore(businessService, cfg.ArtifactStore); err != nil {
		return err
	}
	api := handler.New(businessService, cfg.Identity, logger, authentication)
	business := route.New(api)
	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           newAPIHandler(d, k8sClient, business),
		ReadTimeout:       cfg.Server.ReadTimeout.Std(),
		WriteTimeout:      cfg.Server.WriteTimeout.Std(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return runCtx },
	}

	servers := []*http.Server{srv}
	if cfg.Executor.Enabled {
		tlsConfig, err := executor.NewTLSConfig(cfg.Executor)
		if err != nil {
			return fmt.Errorf("初始化执行器 mTLS: %w", err)
		}
		servers = append(servers, &http.Server{Addr: cfg.Executor.Addr, Handler: route.NewExecutor(api, cfg.Executor),
			TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.Server.ReadTimeout.Std(),
			WriteTimeout: cfg.Server.WriteTimeout.Std(), IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
			BaseContext: func(net.Listener) context.Context { return runCtx }})
	}
	errCh := make(chan error, len(servers))
	var listeners sync.WaitGroup
	for index, server := range servers {
		listeners.Add(1)
		go func() {
			defer listeners.Done()
			var err error
			if index == 0 {
				logger.Info("HTTP 服务监听", "addr", server.Addr)
				err = server.ListenAndServe()
			} else {
				logger.Info("执行器独立 mTLS 监听", "addr", server.Addr)
				err = server.ListenAndServeTLS("", "")
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	var serveErr error
	select {
	case err := <-errCh:
		serveErr = err
	case <-ctx.Done():
		logger.Info("收到退出信号，开始优雅关闭")
	}
	cancelRun()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Std())
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			serveErr = errors.Join(serveErr, fmt.Errorf("优雅关闭超时: %w", err))
		}
	}
	listeners.Wait()
	return serveErr
}
