import request from './request'

export function login(username: string, password: string) {
  return request.post('/api/auth/login', {
    username,
    password,
  })
}

export function fnosLogin() {
  return request.post('/api/auth/fnos/login')
}

export function getFnOSIdentity() {
  return request.get('/api/auth/fnos/identity')
}

export function bindFnOSAccount(mode: 'register' | 'bind', username: string, password: string) {
  return request.post('/api/auth/fnos/bind', { mode, username, password })
}

// 注册。phone 选填——是否显示/是否必填由后端两个系统配置控制
// （register_phone_enabled / register_phone_required），前端按同一份开关渲染。
//
// role（v0.6.10）：注册页「我是房东 / 我是租客」二选一。
//   - 不传 / 'user'：房东（出租人），注册后拥有自己的个人台账；
//   - 'tenant'：租客，注册后处于「等待房东关联」，看不到任何业务数据。
//     租客通道受系统配置 rental_tenant_portal_enabled 控制。
export function register(username: string, password: string, phone = '', role = '') {
  return request.post('/api/auth/register', {
    username,
    password,
    phone,
    ...(role ? { role } : {}),
  })
}

export function checkAuth() {
  return request.get('/api/auth/check')
}

// 退出登录：服务端要落一条「别再自动认人」的标记，网关域上光清本地是退不掉的
// （网关注入的 NAS 身份会让下一个请求把登录态认回来）。它幂等，且 401 属预期
// 分支——会话本来就失效时也必须能退干净，所以跳过全局的「401 即跳登录页」。
//
// bearer：调用方（stores/auth.endSession）在**清本地之前**抓下来的应用令牌。
// 为什么显式带上而不是让 request 拦截器去 store 里取：退出要先把本地登录态清掉
// （见 endSession 的注释），拦截器那时已经取不到令牌；直连端口上不带令牌服务端
// 就认不出人，这次退出会漏掉审计记录。网关域上传空串——按约定不带 Authorization，
// 会话由应用 Cookie / 网关注入身份维持。
export function logout(bearer = '') {
  return request.post('/api/auth/logout', null, {
    headers: {
      'X-Skip-Auth-Redirect': '1',
      ...(bearer ? { Authorization: `Bearer ${bearer}` } : {}),
    },
  })
}

export function getCurrentUser() {
  return request.get('/api/auth/me')
}

export function checkSetupRequired() {
  return request.get('/api/auth/setup-required')
}

export function changePassword(oldPassword: string, newPassword: string) {
  return request.put('/api/auth/password', {
    old_password: oldPassword,
    new_password: newPassword,
  })
}

export function regenerateAPIKey() {
  return request.post('/api/auth/apikey')
}
