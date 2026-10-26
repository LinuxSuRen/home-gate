<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getSettings, saveSettings, getVersion, checkUpdate, upgrade } from '../api'

const feishuAppID = ref('')
const feishuAppSecret = ref('')
const feishuCallback = ref('')
const oidcEndpoints = ref({})
const saving = ref(false)
const sessionTTL = ref(24)
const sessionSaving = ref(false)
const ver = ref(null)
const updateInfo = ref(null)
const checking = ref(false)
const upgrading = ref(false)

onMounted(async () => {
  try {
    const s = await getSettings()
    feishuAppID.value = s.feishu_app_id || ''
    feishuCallback.value = s.feishu_callback || ''
    sessionTTL.value = s.session_ttl_hours || 24
    oidcEndpoints.value = { ...(s.oidc_endpoints || {}), scope: s.oidc_scope || 'openid profile email' }
  } catch (e) {
    ElMessage.error(e.message)
  }
  try {
    ver.value = await getVersion()
  } catch {
    ver.value = null
  }
})

async function doCheckUpdate() {
  checking.value = true
  updateInfo.value = null
  try {
    updateInfo.value = await checkUpdate()
    if (updateInfo.value.container) {
      ElMessage.warning('检测到容器部署，请通过更新镜像升级')
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checking.value = false
  }
}

async function doUpgrade() {
  const target = updateInfo.value && updateInfo.value.latest
  try {
    await ElMessageBox.confirm(
      `将升级到 ${target || '最新版本'}。下载与完整性校验可能需要数分钟，期间请勿关闭页面；` +
        '完成后服务自动重启，短暂不可访问属正常现象。',
      '一键升级',
      { type: 'warning' },
    )
  } catch {
    return
  }
  upgrading.value = true
  try {
    const res = await upgrade()
    ElMessage.success(`已升级到 ${res.upgraded_to || target || '新版本'}，等待服务重启完成…`)
    // 服务重启期间不可达：轮询 /healthz 直到恢复（最长约 1 分钟）再刷新页面
    for (let i = 0; i < 30; i++) {
      try {
        const resp = await fetch('/healthz', { cache: 'no-store' })
        if (resp.ok) break
      } catch {
        /* 重启中，继续等待 */
      }
      await new Promise((resolve) => setTimeout(resolve, 2000))
    }
    location.reload()
  } catch (e) {
    ElMessage.error(e.message)
    upgrading.value = false
  }
}

const endpointLabels = [
  ['authorize', '授权端点'],
  ['token', '令牌端点'],
  ['userinfo', '用户信息端点'],
  ['scope', '推荐 Scope'],
  ['discovery', 'OIDC 发现文档'],
  ['jwks', 'JWKS 公钥'],
]

async function saveSession() {
  if (!sessionTTL.value || sessionTTL.value < 1 || sessionTTL.value > 720) {
    ElMessage.warning('会话时长需在 1～720 小时之间')
    return
  }
  sessionSaving.value = true
  try {
    await saveSettings({ session_ttl_hours: sessionTTL.value })
    ElMessage.success('已保存，新登录的会话将使用新的时长')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    sessionSaving.value = false
  }
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择复制')
  }
}

async function copyCallback() {
  await copyText(feishuCallback.value)
}

async function save() {
  saving.value = true
  try {
    await saveSettings({
      feishu_app_id: feishuAppID.value,
      feishu_app_secret: feishuAppSecret.value,
    })
    feishuAppSecret.value = ''
    ElMessage.success('已保存')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div>
    <h3 style="margin-top: 0">飞书登录</h3>
    <p class="dim">配置飞书开放平台自建应用的 App ID / App Secret 后，登录页将启用「使用飞书登录」。</p>
    <el-form label-width="100px" style="max-width: 640px">
      <el-form-item label="回调地址">
        <el-input :model-value="feishuCallback" readonly>
          <template #append>
            <el-button @click="copyCallback">复制</el-button>
          </template>
        </el-input>
        <div class="dim" style="line-height: 1.6; margin-top: 4px">
          请将此地址加入飞书开放平台应用的「重定向 URL」白名单（安全设置 → 网页），否则飞书登录会报 redirect_uri 不合法。
        </div>
      </el-form-item>
      <el-form-item label="App ID">
        <el-input v-model="feishuAppID" placeholder="cli_xxxxxxxx" />
      </el-form-item>
      <el-form-item label="App Secret">
        <el-input v-model="feishuAppSecret" type="password" show-password placeholder="留空表示不修改" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </el-form-item>
    </el-form>

    <h3>会话设置</h3>
    <p class="dim">登录会话的有效时长（小时），期间访问会自动续期；默认 24 小时（当天有效），对修改后新登录的会话生效。</p>
    <el-form label-width="100px" style="max-width: 640px">
      <el-form-item label="会话时长">
        <el-input-number v-model="sessionTTL" :min="1" :max="720" />
        <span class="dim" style="margin-left: 8px">小时（1～720）</span>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="sessionSaving" @click="saveSession">保存</el-button>
      </el-form-item>
    </el-form>

    <h3>版本与更新</h3>
    <p class="dim">
      当前版本 <b>{{ ver ? (ver.version || 'dev') : '…' }}</b>（{{ ver ? `${ver.goos}/${ver.goarch}` : '' }}）。
      从 GitHub Release 检查并升级；容器部署请通过更新镜像升级。
    </p>
    <el-form label-width="100px" style="max-width: 640px">
      <el-form-item label="最新版本">
        <template v-if="updateInfo">
          <el-tag v-if="updateInfo.has_update" type="warning" size="small">
            {{ updateInfo.latest }} 可升级
          </el-tag>
          <el-tag v-else type="success" size="small">已是最新（{{ updateInfo.latest }}）</el-tag>
          <a
            v-if="updateInfo.release_url"
            :href="updateInfo.release_url"
            target="_blank"
            rel="noopener"
            class="dim"
            style="margin-left: 8px"
          >Release 说明</a>
        </template>
        <span v-else class="dim">未检查</span>
      </el-form-item>
      <el-form-item>
        <el-button :loading="checking" @click="doCheckUpdate">检查更新</el-button>
        <el-button
          v-if="updateInfo && updateInfo.has_update && updateInfo.asset && !updateInfo.container"
          type="primary"
          :loading="upgrading"
          @click="doUpgrade"
        >一键升级</el-button>
      </el-form-item>
    </el-form>

    <h3>应用接入端点</h3>
    <p class="dim">接入本门户的应用（家庭影音、下载站等自托管服务）在 OAuth/OIDC 配置中按此填写。支持 OIDC 自动发现的系统只需配置发现文档地址或 issuer；需显式端点的系统填前三项；Scope 一般填推荐值即可（需 refresh_token 时追加 offline_access）。</p>
    <el-form label-width="120px" style="max-width: 640px">
      <el-form-item v-for="[key, label] in endpointLabels" :key="key" :label="label">
        <el-input :model-value="oidcEndpoints[key]" readonly>
          <template #append>
            <el-button @click="copyText(oidcEndpoints[key])">复制</el-button>
          </template>
        </el-input>
      </el-form-item>
    </el-form>
  </div>
</template>
