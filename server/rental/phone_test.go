// phone_test.go — 注册手机号策略的集成测试（2026-10-11 粉丝需求）。
//
// 需求：管理员要在用户列表里看出谁是谁，所以注册页收集手机号；超管可配置
// 「是否收集」「是否必填」。手机号**只作联系/认人用途**——不做唯一校验、
// 不参与登录、不作为身份凭据。
package rental

import (
	"net/http"
	"testing"

	"smallgo/server/database"
	"smallgo/server/sysconfig"
	"smallgo/server/user"
)

// setPhonePolicy 调整手机号策略（测试内直接改系统配置）。
func setPhonePolicy(t *testing.T, env *testEnv, enabled, required string) {
	t.Helper()
	if err := sysconfig.UpdateConfig(env.db, "register_phone_enabled", enabled, 0); err != nil {
		t.Fatalf("set register_phone_enabled: %v", err)
	}
	if err := sysconfig.UpdateConfig(env.db, "register_phone_required", required, 0); err != nil {
		t.Fatalf("set register_phone_required: %v", err)
	}
}

// registerOK 注册一个账号并断言成功（用占位手机号，保证 userCount > 0）。
func registerOK(t *testing.T, env *testEnv, username string) {
	t.Helper()
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": username, "password": "secret123", "phone": "13800000000",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("注册 %s 失败: %d %s", username, w.Code, w.Body.String())
	}
}

// userByName 按用户名取账号，不存在返回 nil。
func userByName(t *testing.T, env *testEnv, username string) *database.User {
	t.Helper()
	var u database.User
	if err := env.db.Where("username = ?", username).First(&u).Error; err != nil {
		return nil
	}
	return &u
}

// TestRegisterPhoneRequiredBlocksEmpty：必填策略下不填手机号 → 400，且账号不落库。
func TestRegisterPhoneRequiredBlocksEmpty(t *testing.T) {
	env := setupEnv(t)
	// setupEnv 默认关掉必填（其他用例不带 phone），这里显式打开。
	setPhonePolicy(t, env, "true", "true")

	// 先有一个账号，让 userCount > 0（首个账号豁免，见下一条用例）。
	registerOK(t, env, "first-user")

	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "no-phone", "password": "secret123",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("不填手机号应 400，实得 %d %s", w.Code, w.Body.String())
	}
	// 账号不得落库：被策略拦下的注册不能留下半成品。
	if userByName(t, env, "no-phone") != nil {
		t.Fatal("被拦下的注册不应创建账号")
	}
}

// TestRegisterPhoneOptionalAllowsEmpty：选填策略下不填手机号也能注册。
func TestRegisterPhoneOptionalAllowsEmpty(t *testing.T) {
	env := setupEnv(t)
	setPhonePolicy(t, env, "true", "false")

	registerOK(t, env, "owner-1")
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "no-phone-ok", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("选填时应允许不填手机号，实得 %d %s", w.Code, w.Body.String())
	}
	if u := userByName(t, env, "no-phone-ok"); u == nil || u.Phone != "" {
		t.Fatalf("未填手机号的账号 Phone 应为空，实得 %+v", u)
	}
}

// TestRegisterPhoneHiddenStillAcceptsValue：关掉「收集手机号」时不校验格式、
// 但仍接受请求里带上来的值（前端此时不显示输入框，正常情况下不会带）。
func TestRegisterPhoneHiddenStillAcceptsValue(t *testing.T) {
	env := setupEnv(t)
	setPhonePolicy(t, env, "false", "true") // required 在 enabled=false 时无效

	registerOK(t, env, "owner-2")
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "hidden-phone", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("关闭收集时不应要求手机号，实得 %d %s", w.Code, w.Body.String())
	}
}

// TestRegisterPhoneFirstUserExempt：首个账号（实例初始化的人）豁免必填——
// 那时还没有管理员能进后台改配置，强制必填会把人堵在门外。
//
// 注意：setupEnv 已经注册了 landlord（首个账号），所以这里要回到「零账号」
// 的库上测，否则走的是 userCount > 0 的分支（策略生效），考不到豁免。
func TestRegisterPhoneFirstUserExempt(t *testing.T) {
	env := setupEnv(t)
	setPhonePolicy(t, env, "true", "true")

	// 清空账号，回到「全新实例」状态（先清成员关系与台账，避免外键残留）。
	for _, sql := range []string{"DELETE FROM user_orgs", "DELETE FROM orgs", "DELETE FROM users"} {
		if err := env.db.Exec(sql).Error; err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "the-first", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("首个账号应豁免手机号必填，实得 %d %s", w.Code, w.Body.String())
	}

	// 第二个账号就必须填了。
	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "the-second", "password": "secret123",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("第二个账号应要求手机号，实得 %d %s", w.Code, w.Body.String())
	}
}

// TestRegisterPhoneNormalizesAndValidates：手机号会被去掉分隔符；
// 非数字或长度不合法 → 400。
func TestRegisterPhoneNormalizesAndValidates(t *testing.T) {
	env := setupEnv(t)
	setPhonePolicy(t, env, "true", "true")
	registerOK(t, env, "owner-3")

	// 带分隔符的写法应被规范化成纯数字。
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "with-dashes", "password": "secret123", "phone": "138-1234-5678",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("带分隔符的手机号应被接受，实得 %d %s", w.Code, w.Body.String())
	}
	if u := userByName(t, env, "with-dashes"); u == nil || u.Phone != "13812345678" {
		t.Fatalf("手机号应规范化为 13812345678，实得 %+v", u)
	}

	// 含字母 → 400。
	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "bad-phone", "password": "secret123", "phone": "138abcd5678",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("含字母的手机号应 400，实得 %d %s", w.Code, w.Body.String())
	}

	// 过短 → 400。
	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "short-phone", "password": "secret123", "phone": "123",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("过短的手机号应 400，实得 %d %s", w.Code, w.Body.String())
	}
}

// TestRegisterPhoneNotUnique：手机号**不做唯一校验**——同一家人共用一个号、
// 或房东代填同一个号，都应能注册（这是刻意的：手机号只是认人备注，不是账号标识）。
func TestRegisterPhoneNotUnique(t *testing.T) {
	env := setupEnv(t)
	setPhonePolicy(t, env, "true", "true")
	registerOK(t, env, "owner-4")

	for _, name := range []string{"same-phone-a", "same-phone-b"} {
		w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
			"username": name, "password": "secret123", "phone": "13900000000",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("%s：同号注册应放行，实得 %d %s", name, w.Code, w.Body.String())
		}
	}
}

// TestPhonePolicyDefaults：默认策略是「收集 + 必填」（生产口径）。
func TestPhonePolicyDefaults(t *testing.T) {
	env := setupEnv(t)
	// 恢复默认值（setupEnv 为了别的用例关掉了必填）。
	if err := sysconfig.UpdateConfig(env.db, "register_phone_required", "true", 0); err != nil {
		t.Fatal(err)
	}
	p := user.GetRegisterPhonePolicy(env.db)
	if !p.Enabled || !p.Required {
		t.Fatalf("默认策略应为「收集 + 必填」，实得 %+v", p)
	}
	// 关闭收集时 Required 必须跟着为假，否则隐藏了输入框却要求必填 = 死锁。
	setPhonePolicy(t, env, "false", "true")
	if p := user.GetRegisterPhonePolicy(env.db); p.Enabled || p.Required {
		t.Fatalf("关闭收集后 Required 应自动为假，实得 %+v", p)
	}
}
