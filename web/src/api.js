// 统一 HTTP 封装：超时、网络错误包装、错误 envelope 解包。
export class ApiError extends Error {
  constructor(message, code) {
    super(message)
    this.code = code
  }
}

async function request(path, options = {}) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 15000)
  try {
    const resp = await fetch(path, {
      ...options,
      signal: controller.signal,
      headers: {
        ...(options.body ? { 'Content-Type': 'application/json' } : {}),
        ...(options.headers || {}),
      },
    })
    let body = null
    const text = await resp.text()
    if (text) {
      try {
        body = JSON.parse(text)
      } catch {
        body = null
      }
    }
    if (!resp.ok) {
      const err = body && body.error
      throw new ApiError(
        (err && err.message) || `请求失败（HTTP ${resp.status}）`,
        (err && err.code) || `http_${resp.status}`,
      )
    }
    return body && body.data !== undefined ? body.data : body
  } catch (e) {
    if (e instanceof ApiError) throw e
    if (e.name === 'AbortError') throw new ApiError('请求超时，请检查网络后重试', 'timeout')
    throw new ApiError('无法连接后端服务，请确认服务已启动', 'network')
  } finally {
    clearTimeout(timer)
  }
}

// ---- 认证 ----
export const login = (username, password) =>
  request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
export const logout = () => request('/api/auth/logout', { method: 'POST' })
export const getMe = () => request('/api/auth/me')
export const changePassword = (oldPassword, newPassword) =>
  request('/api/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
  })
export const feishuLoginURL = (next) =>
  `/api/auth/feishu/start${next ? `?next=${encodeURIComponent(next)}` : ''}`
export const getFeishuEnabled = () =>
  request('/api/auth/feishu/enabled').then((d) => !!d.enabled)

// ---- 应用入口 ----
export const getApps = () => request('/api/apps')

// ---- 门户关注 ----
export const getFavorites = () => request('/api/favorites')
export const followEntry = (kind, ref) =>
  request(`/api/favorites/${kind}/${ref}`, { method: 'PUT' })
export const unfollowEntry = (kind, ref) =>
  request(`/api/favorites/${kind}/${ref}`, { method: 'DELETE' })

// ---- 管理端：用户 ----
export const getUsers = () => request('/api/admin/users')
export const createUser = (user) =>
  request('/api/admin/users', { method: 'POST', body: JSON.stringify(user) })
export const updateUser = (id, patch) =>
  request(`/api/admin/users/${id}`, { method: 'PUT', body: JSON.stringify(patch) })
export const deleteUser = (id) => request(`/api/admin/users/${id}`, { method: 'DELETE' })
export const resetPassword = (id, password) =>
  request(`/api/admin/users/${id}/reset-password`, {
    method: 'POST',
    body: JSON.stringify({ password }),
  })

// ---- 管理端：用户组 ----
export const getGroups = () => request('/api/admin/groups')
export const createGroup = (group) =>
  request('/api/admin/groups', { method: 'POST', body: JSON.stringify(group) })
export const deleteGroup = (id) => request(`/api/admin/groups/${id}`, { method: 'DELETE' })

// ---- 管理端：链接（纯链接应用入口） ----
export const getLinks = () => request('/api/admin/links')
export const createLink = (link) =>
  request('/api/admin/links', { method: 'POST', body: JSON.stringify(link) })
export const updateLink = (id, patch) =>
  request(`/api/admin/links/${id}`, { method: 'PUT', body: JSON.stringify(patch) })
export const deleteLink = (id) => request(`/api/admin/links/${id}`, { method: 'DELETE' })

// ---- 管理端：客户端 ----
export const getClients = () => request('/api/admin/clients')
export const createClient = (meta) =>
  request('/api/admin/clients', { method: 'POST', body: JSON.stringify(meta) })
export const updateClient = (id, patch) =>
  request(`/api/admin/clients/${id}`, { method: 'PUT', body: JSON.stringify(patch) })
export const resetClientSecret = (id) =>
  request(`/api/admin/clients/${id}/reset-secret`, { method: 'POST' })

// ---- 管理端：initial access token ----
export const getInitialTokens = () => request('/api/admin/initial-tokens')
export const createInitialToken = (remark, days) =>
  request('/api/admin/initial-tokens', {
    method: 'POST',
    body: JSON.stringify({ remark, days }),
  })
export const revokeInitialToken = (id) =>
  request(`/api/admin/initial-tokens/${id}`, { method: 'DELETE' })

// ---- 管理端：审计 / 设置 ----
export const getAudit = (page, pageSize, action, actor) => {
  const q = new URLSearchParams({ page, page_size: pageSize })
  if (action) q.set('action', action)
  if (actor) q.set('actor', actor)
  return request(`/api/admin/audit?${q}`)
}
export const getSettings = () => request('/api/admin/settings')
export const saveSettings = (settings) =>
  request('/api/admin/settings', { method: 'PUT', body: JSON.stringify(settings) })
