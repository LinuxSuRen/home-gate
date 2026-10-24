<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { getSettings, saveSettings } from '../api'

const feishuAppID = ref('')
const feishuAppSecret = ref('')
const feishuCallback = ref('')
const oidcEndpoints = ref({})
const saving = ref(false)

onMounted(async () => {
  try {
    const s = await getSettings()
    feishuAppID.value = s.feishu_app_id || ''
    feishuCallback.value = s.feishu_callback || ''
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
