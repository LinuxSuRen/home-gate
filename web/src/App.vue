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

  <el-container v-else-if="!me" style="min-height: 100vh">
    <el-header class="app-header">
      <div class="app-header__brand">
        <span class="app-header__title">Home Gate</span>
      </div>
      <div class="app-header__user">
        <el-button type="primary" @click="goLogin">登录</el-button>
      </div>
    </el-header>
    <el-main>
      <PortalView anonymous @login="goLogin" />
    </el-main>
  </el-container>

  <template v-else>
    <el-container style="min-height: 100vh">
      <el-header class="app-header">
        <div class="app-header__brand">
          <span class="app-header__title">Home Gate</span>
          <el-button
            v-if="isAdmin"
            :text="true"
            :type="adminTab ? 'primary' : ''"
            @click="adminTab = !adminTab"
          >
            {{ adminTab ? '返回门户' : '管理后台' }}
          </el-button>
        </div>
        <div class="app-header__user">
          <el-dropdown @command="handleUserCommand">
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
        <AdminView v-if="adminTab && isAdmin" />
        <PortalView v-else :me="me" />
      </el-main>
    </el-container>

    <el-dialog v-model="showChangePwd" title="修改密码" width="420px" :close-on-click-modal="!me.must_change_password" :show-close="!me.must_change_password">
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
  gap: 16px;
}
.app-header__title {
  font-size: 16px;
  font-weight: 600;
}
.app-header__user {
  display: flex;
  align-items: center;
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
