package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"smallgo/server/apps"
	"smallgo/server/audit"
	"smallgo/server/backup"
	"smallgo/server/config"
	"smallgo/server/database"
	"smallgo/server/logger"
	"smallgo/server/middleware"
	"smallgo/server/realtime"
	"smallgo/server/response"
	"smallgo/server/scheduler"
	"smallgo/server/security"
	"smallgo/server/stats"
	"smallgo/server/sysconfig"
	"smallgo/server/update"
	"smallgo/server/upload"
	"smallgo/server/user"
	"smallgo/server/version"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// preUpgradeSnapshot 在结构迁移之前给数据库留一份快照。
//
// 返回值：快照文件名（空串=本次不需要快照）、判定原因、错误。
// 调用点见 Start() 中 AutoMigrate 之前。
//
// 判定规则刻意不依赖 upgrade_records —— 那个表只记录**版本化升级**，
// 而 AutoMigrate 补列、apps.Migrate 等结构变化都不写它；开发版二进制
// （版本恒为 dev）更是永远不写，拿它当判据会导致每次重启都快照一份，
// 把用户的保留空间吃光。改用「本机记一个快照标记」：
//
//   - 全新安装（库里还没有 users 表）→ 没有数据可丢，不快照（但仍写下标记，
//     否则这个二进制之后每次重启都会被当成"版本变更"，白白吃用户的保留空间）；
//   - 本二进制已经快照过（标记里的指纹相同）→ 结构这次没变，不快照；
//   - 否则（换了新二进制 = 有升级）→ 先快照再迁移，并记下指纹。
//
// 指纹取版本 + 构建时间 + 提交号，正式发版三者随版本变；同一版本的反复
// 重启不会重复快照，但只要换版本就会重新兜底一次。
func preUpgradeSnapshot(db *gorm.DB, dataDir, appVersion string) (string, string, error) {
	fingerprint := binaryFingerprint()

	// 全新安装判定：连 users 表都没有，说明这是一次初始化，没有东西可丢。
	var userTables int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'",
	).Scan(&userTables).Error; err != nil {
		return "", "", err
	}
	if userTables == 0 {
		writeSnapshotMarker(dataDir, fingerprint)
		return "", "全新安装，无需快照", nil
	}

	marker := filepath.Join(dataDir, snapshotMarkerFile)
	if raw, err := os.ReadFile(marker); err == nil {
		if strings.TrimSpace(string(raw)) == fingerprint {
			return "", "本版本已快照过，无需重复", nil
		}
	}

	name, err := backup.Create(db, dataDir)
	if err != nil {
		return "", "", err
	}
	// 标记写失败不影响快照本身已生效，只影响下次可能多快照一份。
	writeSnapshotMarker(dataDir, fingerprint)
	return name, "检测到版本变更，先留一份升级前快照", nil
}

// writeSnapshotMarker 记下「这个二进制已经处理过了」。写失败只记日志：
// 标记没了的后果至多是下次多快照一份，不会造成数据问题。
func writeSnapshotMarker(dataDir, fingerprint string) {
	if err := os.WriteFile(filepath.Join(dataDir, snapshotMarkerFile), []byte(fingerprint), 0o644); err != nil {
		logger.Error("写入快照标记失败（下次启动会重复快照）: %v", err)
	}
}

// snapshotMarkerFile 记录「哪个二进制已经快照过」的标记文件名。
const snapshotMarkerFile = ".last_preupgrade_snapshot"

// binaryFingerprint 当前二进制的身份指纹：版本 + 构建时间 + 提交号。
func binaryFingerprint() string {
	return strings.Join([]string{version.Version, version.BuildTime, version.GitCommit}, "|")
}

func Start() {
	cfg := config.C

	if cfg.ShowHelp {
		config.PrintHelp()
		os.Exit(0)
	}

	if cfg.ShowVersion {
		version.PrintVersion()
		os.Exit(0)
	}

	if cfg.ResetAdminPassword {
		db, err := database.InitDB(cfg.DBPath)
		if err != nil {
			fmt.Printf("Failed to connect database: %v\n", err)
			os.Exit(1)
		}
		defer database.CloseDB(db)

		if err := user.ResetAdminPassword(db); err != nil {
			fmt.Printf("Failed to reset admin password: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if cfg.LogMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	if err := logger.Init(cfg.LogDir, cfg.LogRetentionDays, cfg.LogConsole); err != nil {
		fmt.Printf("Failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	db, err := database.InitDB(cfg.DBPath)
	if err != nil {
		fmt.Printf("Failed to connect database: %v\n", err)
		os.Exit(1)
	}
	defer database.CloseDB(db)

	// 升级前自动快照（数据安全底线，2026-10-12 加）。
	//
	// 为什么必须放在 AutoMigrate/RunUpgrades **之前**：这两步会改表结构与数据，
	// 一旦某次升级写错了，房东辛苦录入的房源、租户、账单就没有任何回退点——
	// 每日 03:00 的自动备份最多也只能兜住 24 小时内的数据，而且它在
	// backup_auto_enabled 被关掉时根本不跑。
	//
	// 判定「要不要快照」很关键，否则会在每次正常启动都塞一份没用的备份、
	// 把用户的保留空间吃光：
	//   - 全新安装（库里一张表都没有）→ 不快照，没有东西可丢；
	//   - 库版本已等于当前二进制 → 结构不会变，不快照；
	//   - 库版本低于当前二进制（有升级待执行）→ 先快照再迁移。
	// 快照沿用 backup.Create 的命名规则，因此它会**直接出现在备份管理页的
	// 列表里**，用户可以下载、也可以一键回滚。
	if snapshot, reason, err := preUpgradeSnapshot(db, cfg.DataDir, version.Version); err != nil {
		// 快照失败**不能**阻止启动：挡住启动只会让用户彻底用不了应用，
		// 那比没有快照更糟。这里只记日志，让人知道没兜底。
		logger.Error("升级前自动快照失败（将继续启动）: %v", err)
	} else if snapshot != "" {
		logger.Info("升级前自动快照 %s（%s）", snapshot, reason)
	}

	if err := database.AutoMigrate(db); err != nil {
		fmt.Printf("Failed to migrate database: %v\n", err)
		os.Exit(1)
	}

	if err := database.RunUpgrades(db, version.Version, database.Upgrades); err != nil {
		fmt.Printf("Failed to run upgrades: %v\n", err)
		os.Exit(1)
	}

	if err := apps.MigrateAll(db); err != nil {
		fmt.Printf("Failed to run app migrations: %v\n", err)
		os.Exit(1)
	}

	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		fmt.Printf("Failed to init default configs: %v\n", err)
		os.Exit(1)
	}

	logger.SetRetentionFunc(func() int {
		val, err := sysconfig.GetConfig(db, "log_retention_days", 0)
		if err != nil || val == "" {
			return cfg.LogRetentionDays
		}
		if days, err := strconv.Atoi(val); err == nil {
			return days
		}
		return cfg.LogRetentionDays
	})
	// Keep database-backed operation history aligned with the administrator's
	// log retention policy. The job is registered before scheduler.Start.
	audit.RegisterCleanup(db)

	jwtSecret, err := sysconfig.GetConfig(db, "jwt_secret", 0)
	if err != nil || jwtSecret == "" {
		fmt.Println("Failed to get JWT secret")
		os.Exit(1)
	}

	r := NewRouter(cfg, db, jwtSecret)

	// 匿名使用统计心跳：默认开启、应用内不提供关闭入口（2026-10-11 用户定调），
	// 只有部署层面的 -disable-stats / DISABLE_STATS=1 能整体退出。
	if !cfg.DisableStats {
		stats.Start(version.AppName, version.Version, cfg.DeviceType, cfg.DataDir)
		defer stats.Stop()
	}

	// Start background jobs registered by the framework or apps.
	sched := scheduler.Start()
	defer sched.Stop()

	logger.Info("========================================")
	logger.Info("  %s %s", version.AppName, version.Version)
	logger.Info("========================================")
	logger.Info("  Data Dir:    %s", cfg.DataDir)
	logger.Info("  Database:    %s", cfg.DBPath)
	logger.Info("  Web Dir:     %s", cfg.WebDir)
	logger.Info("  Upload Dir:  %s", cfg.UploadDir)
	if cfg.FnOSApp {
		logger.Info("  Gateway:     %s", cfg.GatewayPrefix)
		logger.Info("  Socket:      %s", cfg.GatewaySocket)
		logger.Info("  Access URL:  http://localhost:%d", cfg.Port)
	} else {
		logger.Info("  Access URL:  http://localhost:%d", cfg.Port)
	}
	if cfg.RateLimit > 0 {
		logger.Info("  Rate Limit:  %d req/min per IP", cfg.RateLimit)
	}
	logger.Info("========================================")

	listeners, err := listen(cfg)
	if err != nil {
		logger.Error("Failed to listen: %v", err)
		os.Exit(1)
	}
	for _, listener := range listeners {
		defer listener.Listener.Close()
	}
	if cfg.FnOSApp {
		defer os.Remove(cfg.GatewaySocket)
	}

	servers := make([]*http.Server, 0, len(listeners))
	for _, listener := range listeners {
		handler := http.Handler(r)
		if !listener.IsFnOSGateway {
			handler = newDirectHandler(r, cfg)
		}
		srv := newHTTPServer(handler, listener.IsFnOSGateway)
		servers = append(servers, srv)
		go func(srv *http.Server, listener net.Listener) {
			if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
				logger.Error("Server error: %v", err)
				os.Exit(1)
			}
		}(srv, listener.Listener)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, srv := range servers {
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("Server forced to shutdown: %v", err)
		}
	}

	apps.ShutdownAll()

	logger.Info("Server exited")
}

// newDirectHandler keeps the configured TCP port useful in fnOS mode while
// preserving the single canonical gateway-prefixed SPA URL. The browser is
// redirected before loading assets, so Vite's /app/<appname>/ base is intact.
func newDirectHandler(handler http.Handler, cfg config.Config) http.Handler {
	if !cfg.FnOSApp {
		return handler
	}
	prefix := strings.TrimSuffix(cfg.GatewayPrefix, "/") + "/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, prefix, http.StatusTemporaryRedirect)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

type listenerBinding struct {
	Listener      net.Listener
	IsFnOSGateway bool
}

func newHTTPServer(handler http.Handler, fnOSGateway bool) *http.Server {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if fnOSGateway {
		srv.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
			return user.MarkFnOSGateway(ctx)
		}
	}
	return srv
}

func listen(cfg config.Config) ([]listenerBinding, error) {
	tcpListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, err
	}
	listeners := []listenerBinding{{Listener: tcpListener}}
	if !cfg.FnOSApp {
		return listeners, nil
	}
	if cfg.GatewaySocket == "" {
		tcpListener.Close()
		return nil, fmt.Errorf("-fnos-app requires -gateway-socket")
	}
	if !strings.HasPrefix(cfg.GatewayPrefix, "/app/") {
		tcpListener.Close()
		return nil, fmt.Errorf("-gateway-prefix must begin with /app/")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.GatewaySocket), 0755); err != nil {
		tcpListener.Close()
		return nil, fmt.Errorf("create gateway socket directory: %w", err)
	}
	if err := os.Remove(cfg.GatewaySocket); err != nil && !os.IsNotExist(err) {
		tcpListener.Close()
		return nil, fmt.Errorf("remove stale gateway socket: %w", err)
	}
	listener, err := net.Listen("unix", cfg.GatewaySocket)
	if err != nil {
		tcpListener.Close()
		return nil, fmt.Errorf("listen on gateway socket: %w", err)
	}
	return append(listeners, listenerBinding{Listener: listener, IsFnOSGateway: true}), nil
}

// NewRouter builds the full API router (all middleware, routes and registered
// apps) without starting an HTTP listener. Start uses it in production; tests
// use it to exercise the API against an in-memory database.
func NewRouter(cfg config.Config, db *gorm.DB, jwtSecret string) *gin.Engine {
	r := gin.New()
	r.Use(middleware.Logger())
	r.Use(gin.Recovery())
	r.Use(middleware.CORS(cfg.CORSOrigin))
	r.Use(middleware.LimitJSONBody(1 << 20))

	gatewayPrefix := ""
	appGroup := r.Group("")
	if cfg.FnOSApp {
		gatewayPrefix = strings.TrimSuffix(cfg.GatewayPrefix, "/")
		appGroup = r.Group(gatewayPrefix)
	}
	api := appGroup.Group("/api")

	api.GET("/version", func(c *gin.Context) {
		response.Success(c, version.GetVersion())
	})

	// Liveness/readiness probe for Docker, NAS health checks, uptime monitors.
	api.GET("/health", func(c *gin.Context) {
		response.Success(c, gin.H{
			"status":  "ok",
			"version": version.Version,
			"time":    time.Now().Format(time.RFC3339),
		})
	})

	// Rate limiter for public endpoints
	var publicLimiter gin.HandlerFunc
	if cfg.RateLimit > 0 {
		rl := middleware.NewRateLimiter(cfg.RateLimit, 1*time.Minute)
		publicLimiter = middleware.LimitRequests(rl)
	} else {
		publicLimiter = middleware.LimitRequests(nil)
	}

	// Auth route groups shared by all features.
	publicGroup := api.Group("", publicLimiter)
	optionalAuthGroup := api.Group("")
	optionalAuthGroup.Use(middleware.OptionalAuth(jwtSecret, db))
	authGroup := api.Group("")
	authGroup.Use(middleware.RequireAuth(jwtSecret, db))
	authGroup.Use(audit.MutationLogger(db))
	adminGroup := api.Group("")
	adminGroup.Use(middleware.RequireAuth(jwtSecret, db))
	adminGroup.Use(middleware.RequireAdmin())
	adminGroup.Use(audit.MutationLogger(db))

	// Config: public read is rate-limited; user config read & write require
	// auth; system config read & metadata require admin.
	sysconfig.RegisterRoutes(publicGroup, authGroup, adminGroup, db)

	// Security questions: forgot-password flow is public (rate limited); managing
	// one's own questions requires auth.
	security.RegisterRoutes(publicGroup, authGroup, db)

	// File upload requires authentication; uploaded content is still readable by
	// URL so it can be embedded in public pages.
	authGroup.POST("/upload", upload.HandleUpload(cfg.UploadDir))
	appGroup.GET("/uploads/*filepath", upload.ServeUpload(cfg.UploadDir))

	user.RegisterRoutes(publicGroup, optionalAuthGroup, authGroup, adminGroup, db, cfg.FnOSApp, gatewayPrefix)

	// Server-Sent Events stream for the default realtime broker. Apps push
	// live updates via realtime.Publish; browsers subscribe at /api/realtime.
	authGroup.GET("/realtime", realtime.Handler())

	// 版本检查（升级提示条）：需要登录，检查失败优雅降级。
	update.RegisterRoutes(authGroup)
	// 一键自更新（下载发行包 → 替换自身 → 原地重启）只允许管理员触发
	update.RegisterApplyRoutes(adminGroup)

	// 赞赏支持计数：需要登录（前端入口仅对管理员显示）。
	stats.RegisterRoutes(authGroup, db)

	// 数据库备份管理：仅管理员。
	backup.RegisterRoutes(adminGroup, db, cfg.DataDir)

	// Admin operation log (browse + CSV export).
	audit.RegisterRoutes(adminGroup, db)

	// Admin upgrade history: every applied one-off data migration, newest first.
	adminGroup.GET("/upgrade-history", func(c *gin.Context) {
		records, err := database.ListUpgradeRecords(db)
		if err != nil {
			response.ErrorInternal(c, "读取升级历史失败")
			return
		}
		response.Success(c, records)
	})

	// Register apps from the registry (includes the qrcode example app).
	apps.SetupAll(publicGroup, db)
	apps.SetupProtectedAll(authGroup, adminGroup, db)

	if cfg.WebDir != "" {
		appGroup.Static("/assets", cfg.WebDir+"/assets")
		appGroup.StaticFile("/favicon.ico", cfg.WebDir+"/favicon.ico")
		appGroup.StaticFile("/favicon.svg", cfg.WebDir+"/favicon.svg")
		appGroup.StaticFile("/favicon-16.png", cfg.WebDir+"/favicon-16.png")
		appGroup.StaticFile("/favicon-32.png", cfg.WebDir+"/favicon-32.png")
		appGroup.StaticFile("/favicon-192.png", cfg.WebDir+"/favicon-192.png")
		appGroup.StaticFile("/favicon-512.png", cfg.WebDir+"/favicon-512.png")
		appGroup.StaticFile("/apple-touch-icon.png", cfg.WebDir+"/apple-touch-icon.png")
		appGroup.GET("/manifest.webmanifest", func(c *gin.Context) {
			c.Header("Content-Type", "application/manifest+json")
			c.File(cfg.WebDir + "/manifest.webmanifest")
		})
		r.NoRoute(func(c *gin.Context) {
			if !cfg.FnOSApp || c.Request.URL.Path == strings.TrimSuffix(cfg.GatewayPrefix, "/") || strings.HasPrefix(c.Request.URL.Path, strings.TrimSuffix(cfg.GatewayPrefix, "/")+"/") {
				serveSPA(c, cfg)
				return
			}
			c.Status(http.StatusNotFound)
		})
	}

	return r
}

// serveSPA 返回 SPA 入口 HTML，并把 __FNOS_GATEWAY_FLAG__ 替换成「这次文档
// 请求是不是从飞牛网关 socket 进来的」：只有服务端知道答案（两条监听器分别
// 是网关 socket 与直连端口），而前端必须知道——网关域上不能带应用自己的
// Authorization，会被接入层当成无效会话 token 直接拒掉（200 "invalid token"，
// 请求到不了应用），见 web/src/utils/gateway.ts。占位符只出现在字符串字面量里
// （window.__FNOS_GATEWAY__ = "__FNOS_GATEWAY_FLAG__" === "true"），
// 这样开发服务器不替换时它就是 false，也不会误伤属性名。
func serveSPA(c *gin.Context, cfg config.Config) {
	data, err := os.ReadFile(filepath.Join(cfg.WebDir, "index.html"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	page := strings.ReplaceAll(string(data), "__FNOS_GATEWAY_FLAG__", strconv.FormatBool(middleware.OnFnOSGateway(c.Request.Context())))
	// 入口 HTML 禁止强缓存：升级后浏览器必须拿到新的 index.html（从而引用新的
	// hash 资源），否则用户看到旧前端还以为升级没生效。no-cache 是协商缓存，
	// 文件没变时 304，性能无影响。
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}
