package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type visitor struct {
	count   int
	expires time.Time
}

type RateLimiter struct {
	mu          sync.Mutex
	visitors    map[string]*visitor
	rate        int
	window      time.Duration
	nextCleanup time.Time
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors:    make(map[string]*visitor),
		rate:        rate,
		window:      window,
		nextCleanup: time.Now().Add(window),
	}
	return rl
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if !now.Before(rl.nextCleanup) {
		for key, item := range rl.visitors {
			if !now.Before(item.expires) {
				delete(rl.visitors, key)
			}
		}
		rl.nextCleanup = now.Add(rl.window)
	}

	v, exists := rl.visitors[ip]
	if !exists || !now.Before(v.expires) {
		rl.visitors[ip] = &visitor{count: 1, expires: now.Add(rl.window)}
		return true
	}

	if v.count >= rl.rate {
		return false
	}

	v.count++
	return true
}

// rateLimitKey 限流桶的键。
//
// 飞牛网关形态下应用监听的是 unix socket，请求的 RemoteAddr 没有 host:port，
// gin 的 ClientIP() 返回空串——所有网关用户会挤进同一个桶，默认 20 次/分被
// 一个人打满，全站公开接口（登录、注册、租客自助登记）一起 429（2026-10-11
// 实测：第 21 个请求起全员被拒、且窗口内一直拒）。因此网关域上优先用网关注入
// 的 NAS 用户标识分桶；未登录 NAS 的匿名流量（登录页/登记页首次打开）退回固定
// 键，与同网关的匿名请求共享一个桶——这部分无法区分来源，只能靠窗口值兜底。
func rateLimitKey(c *gin.Context) string {
	// gin 1.12 对 unix socket 连接会把 ClientIP() 算成字符串 "<nil>"
	// （net.IP(nil).String()，见 gin context.go 的 trusted 分支）：看着非空、
	// 其实所有网关请求共用这一个值，等于没有区分度，所以单独排掉。
	if ip := c.ClientIP(); ip != "" && ip != "<nil>" {
		return ip
	}
	// 网关 socket：只能靠网关注入的 NAS 用户标识分桶。该标识仅在 socket 连接上
	// 可信（直连端口上客户端可以自己伪造 X-Trim-Userid），因此只对网关连接采用，
	// 其余（含网关上的未登录匿名流量）统一归入固定键。
	if OnFnOSGateway(c.Request.Context()) {
		if uid := strings.TrimSpace(c.GetHeader("X-Trim-Userid")); uid != "" {
			return "nas-user-" + uid
		}
	}
	return "nas-anonymous"
}

func LimitRequests(rl *RateLimiter) gin.HandlerFunc {
	if rl == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if !rl.Allow(rateLimitKey(c)) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "too many requests, please try again later",
				"data":    nil,
			})
			return
		}
		c.Next()
	}
}
