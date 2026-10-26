<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { ArrowDown } from '@element-plus/icons-vue'
import {
  getMe, logout, changePassword, feishuLoginURL,
} from './api'
import LoginView from './views/LoginView.vue'
import PortalView from './views/PortalView.vue'
import AdminView from './views/AdminView.vue'

const loading = ref(true)
const me = ref(null)
const adminTab = ref(false)
const nextURL = ref('/')
const showLogin = ref(false)
const showChangePwd = ref(false)
const pwdForm = ref({ old_password: '', new_password: '', confirm: '' })

const isAdmin = computed(() => !!(me.value && me.value.groups || []).includes('admin'))

onMounted(async () => {
  const params = new URLSearchParams(location.search)
  nextURL.value = params.get('next') || '/'
  // 未登录时默认以游客身份浏览门户；显式访问 /login 或带回跳目标（如 OAuth 授权）时直接进登录页
  showLogin.value = location.pathname === '/login' || nextURL.value !== '/'
  try {
    me.value = await getMe()
  } catch {
    me.value = null
  }
  loading.value = false
  if (me.value && me.value.must_change_password) {
    showChangePwd.value = true
  }
})

function goLogin() {
  showLogin.value = true
  history.replaceState(null, '', '/login' + location.search)
}

function backToPortal() {
  showLogin.value = false
  nextURL.value = '/'
  history.replaceState(null, '', '/')
}

async function handleLoginSuccess(user) {
  me.value = user
  if (user.must_change_password) {
    showChangePwd.value = true
    return
  }
  goNext()
}

function goNext() {
  const target = nextURL.value
  history.replaceState(null, '', '/')
  if (target !== '/' && target.startsWith('/')) {
    location.href = target
  }
}

function handleUserCommand(cmd) {
  if (cmd === 'change-password') showChangePwd.value = true
  else if (cmd === 'logout') handleLogout()
}

async function handleLogout() {
  try {
    await logout()
  } catch {
    /* 会话过期也继续跳转 */
  }
  me.value = null
  adminTab.value = false
  showLogin.value = false
}

async function handleChangePwd() {
  const f = pwdForm.value
  if (!f.new_password || f.new_password.length < 8) {
    ElMessage.warning('新密码至少 8 位')
    return
  }
  if (f.new_password !== f.confirm) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  try {
    await changePassword(f.old_password, f.new_password)
    ElMessage.success('密码已修改')
    showChangePwd.value = false
    me.value = { ...me.value, must_change_password: false }
    pwdForm.value = { old_password: '', new_password: '', confirm: '' }
    if (nextURL.value !== '/') goNext()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<template>
  <div v-if="loading" class="page dim">正在加载…</div>

  <LoginView
    v-else-if="!me && showLogin"
    :next="nextURL"
    @success="handleLoginSuccess"
    @cancel="backToPortal"
  />

  <el-container v-else style="min-height: 100vh">
    <el-header class="app-header">
      <div class="app-header__brand">
        <img class="app-header__logo" src="/logo.svg" alt="Home Gate logo" />
        <span class="app-header__title">Home Gate</span>
        <el-button
          v-if="me && isAdmin"
          :text="true"
          :type="adminTab ? 'primary' : ''"
          @click="adminTab = !adminTab"
        >
          {{ adminTab ? '返回门户' : '管理后台' }}
        </el-button>
      </div>
      <div class="app-header__user">
        <a
          class="gh-link"
          href="https://github.com/linuxsuren/home-gate"
          target="_blank"
          rel="noopener"
          title="GitHub 仓库"
          aria-label="GitHub 仓库"
        >
          <svg class="gh-link__icon" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
            <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z" />
          </svg>
        </a>
        <el-button v-if="!me" type="primary" @click="goLogin">登录</el-button>
        <el-dropdown v-else @command="handleUserCommand">
          <span class="user-trigger">
            {{ me.name || me.username }}
            <span class="dim user-trigger__groups">{{ (me.groups || []).join('、') || '未分组' }}</span>
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item disabled>
                {{ me.name || me.username }}（{{ (me.groups || []).join('、') || '未分组' }}）
              </el-dropdown-item>
              <el-dropdown-item divided command="change-password">修改密码</el-dropdown-item>
              <el-dropdown-item command="logout">退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </el-header>
    <el-main>
      <AdminView v-if="me && adminTab && isAdmin" />
      <PortalView v-else :me="me" :anonymous="!me" @login="goLogin" />
    </el-main>
  </el-container>

  <el-dialog
    v-if="me"
    v-model="showChangePwd"
    title="修改密码"
    width="420px"
    :close-on-click-modal="!me.must_change_password"
    :show-close="!me.must_change_password"
  >
    <el-alert v-if="me.must_change_password" type="warning" :closable="false" style="margin-bottom: 12px">
      首次登录请先修改初始密码后继续使用。
    </el-alert>
    <el-form label-width="80px">
      <el-form-item label="原密码">
        <el-input v-model="pwdForm.old_password" type="password" show-password placeholder="飞书登录的账号可留空" />
      </el-form-item>
      <el-form-item label="新密码">
        <el-input v-model="pwdForm.new_password" type="password" show-password />
      </el-form-item>
      <el-form-item label="确认新密码">
        <el-input v-model="pwdForm.confirm" type="password" show-password @keyup.enter="handleChangePwd" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button type="primary" @click="handleChangePwd">保存</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--color-bg-panel);
  border-bottom: 1px solid var(--color-border-subtle);
}
.app-header__brand {
  display: flex;
  align-items: center;
  gap: 10px;
}
.app-header__logo {
  width: 24px;
  height: 24px;
}
.app-header__title {
  font-size: 16px;
  font-weight: 600;
}
.app-header__user {
  display: flex;
  align-items: center;
}
.gh-link {
  display: inline-flex;
  margin-right: 16px;
  color: var(--color-text-dim);
}
.gh-link__icon {
  width: 18px;
  height: 18px;
}
.user-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  outline: none;
  font-size: 14px;
  color: var(--color-text-primary);
}
.user-trigger__groups {
  font-size: 12px;
}
</style>
