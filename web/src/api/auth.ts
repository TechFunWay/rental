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

export function register(username: string, password: string) {
  return request.post('/api/auth/register', {
    username,
    password,
  })
}

export function checkAuth() {
  return request.get('/api/auth/check')
}

// 退出登录：服务端要落一条「别再自动认人」的标记，网关域上光清本地是退不掉的
// （网关注入的 NAS 身份会让下一个请求把登录态认回来）。它幂等，且 401 属预期
// 分支——会话本来就失效时也必须能退干净，所以跳过全局的「401 即跳登录页」。
export function logout() {
  return request.post('/api/auth/logout', null, {
    headers: { 'X-Skip-Auth-Redirect': '1' },
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
