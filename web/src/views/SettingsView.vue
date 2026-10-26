<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { getSettings, saveSettings } from '../api'

const feishuAppID = ref('')
const feishuAppSecret = ref('')
const feishuCallback = ref('')
const oidcEndpoints = ref({})
const saving = ref(false)
const sessionTTL = ref(24)
const sessionSaving = ref(false)
const portalDomain = ref('')
const domainSaving = ref(false)

onMounted(async () => {
  try {
    const s = await getSettings()
    feishuAppID.value = s.feishu_app_id || ''
    feishuCallback.value = s.feishu_callback || ''
    sessionTTL.value = s.session_ttl_hours || 24
    portalDomain.value = s.portal_domain || ''
    oidcEndpoints.value = { ...(s.oidc_endpoints || {}), scope: s.oidc_scope || 'openid profile email' }
  } catch (e) {
    ElMessage.error(e.message)
  }
})

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

async function saveDomain() {
  const d = (portalDomain.value || '').trim().toLowerCase()
  if (d && !/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(d)) {
    ElMessage.warning('门户域名须为合法域名（如 tech.abc.com，不含协议与端口）')
    return
  }
  domainSaving.value = true
  try {
    await saveSettings({ portal_domain: d })
    ElMessage.success('已保存')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    domainSaving.value = false
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

    <h3>门户域名</h3>
    <p class="dim">门户对外的域名（如 tech.abc.com）。配置后可在「客户端」「链接」中为服务分配子域名（如 license.tech.abc.com），由 home-gate 自身反向代理访问，无需 nginx 等中间件。需在 DNS 将 <code>*.tech.abc.com</code> 泛解析指向本服务。</p>
    <el-form label-width="100px" style="max-width: 640px">
      <el-form-item label="主域名">
        <el-input v-model="portalDomain" placeholder="如 tech.abc.com（留空则按启动参数 -issuer 推断）" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="domainSaving" @click="saveDomain">保存</el-button>
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
