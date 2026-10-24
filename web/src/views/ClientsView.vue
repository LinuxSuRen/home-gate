<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getClients, createClient, updateClient, resetClientSecret } from '../api'

const clients = ref([])
const loading = ref(true)
const showEdit = ref(false)
const editing = ref(null)
const form = ref({ name: '', description: '', logo_url: '', portal_url: '' })

// ---- 新建客户端（管理员代第三方系统注册） ----
const showCreate = ref(false)
const creating = ref(false)
const createForm = ref({ name: '', redirect_uris: '', auth_method: 'client_secret_basic', scope: 'openid profile email offline_access', portal_url: '', description: '', logo_url: '' })
const createdCreds = ref(null) // { client_id, client_secret, registration_access_token }

function openCreate() {
  createForm.value = { name: '', redirect_uris: '', auth_method: 'client_secret_basic', scope: 'openid profile email offline_access', portal_url: '', description: '', logo_url: '' }
  createdCreds.value = null
  showCreate.value = true
}

async function submitCreate() {
  const f = createForm.value
  const uris = f.redirect_uris.split('\n').map((x) => x.trim()).filter(Boolean)
  if (!f.name || uris.length === 0) {
    ElMessage.warning('名称与回调地址必填')
    return
  }
  creating.value = true
  try {
    createdCreds.value = await createClient({
      client_name: f.name,
      redirect_uris: uris,
      token_endpoint_auth_method: f.auth_method,
      scope: f.scope,
      portal_url: f.portal_url,
      description: f.description,
      logo_url: f.logo_url,
    })
    ElMessage.success('客户端已创建')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    creating.value = false
  }
}

async function load() {
  loading.value = true
  try {
    clients.value = (await getClients()) || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openEdit(c) {
  editing.value = c
  form.value = {
    name: c.name || '',
    description: c.description || '',
    logo_url: c.logo_url || '',
    portal_url: c.portal_url || '',
  }
  showEdit.value = true
}

async function saveEdit() {
  try {
    await updateClient(editing.value.id, {
      name: form.value.name,
      description: form.value.description,
      logo_url: form.value.logo_url,
      portal_url: form.value.portal_url,
    })
    ElMessage.success('已保存')
    showEdit.value = false
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function toggle(c) {
  try {
    await updateClient(c.id, { active: !c.active })
    ElMessage.success(c.active ? '已停用' : '已启用')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function resetSecret(c) {
  try {
    await ElMessageBox.confirm(`重置「${c.name || c.client_id}」的 client secret？子系统需同步更新。`, '重置 secret', { type: 'warning' })
  } catch {
    return
  }
  try {
    const res = await resetClientSecret(c.id)
    await ElMessageBox.alert(`新 client_secret（仅显示一次）：${res.client_secret}`, '重置成功', { confirmButtonText: '知道了' })
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<template>
  <div>
    <p class="dim" style="margin-top: 0">客户端可由各系统通过 DCR 自助注册，也可在此直接新建（适用于自己管理的第三方系统，如 GitLab）；名称 / 描述 / Logo 展示在门户应用卡片上。</p>
    <div style="margin-bottom: 12px">
      <el-button type="primary" @click="openCreate">新建客户端</el-button>
    </div>
    <el-table v-loading="loading" :data="clients">
      <el-table-column prop="name" label="名称" min-width="120" />
      <el-table-column prop="client_id" label="client_id" min-width="200" />
      <el-table-column label="回调地址" min-width="240">
        <template #default="{ row }">
          <div v-for="u in row.redirect_uris" :key="u" class="dim">{{ u }}</div>
        </template>
      </el-table-column>
      <el-table-column prop="token_endpoint_auth_method" label="认证方式" width="160" />
      <el-table-column prop="description" label="描述" min-width="140" />
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag :type="row.active ? 'success' : 'danger'" size="small">{{ row.active ? '启用' : '停用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="240" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" @click="toggle(row)">{{ row.active ? '停用' : '启用' }}</el-button>
          <el-button size="small" :disabled="row.token_endpoint_auth_method === 'none'" @click="resetSecret(row)">重置 secret</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="showCreate" title="新建客户端" width="560px">
      <template v-if="!createdCreds">
        <el-form label-width="90px">
          <el-form-item label="名称">
            <el-input v-model="createForm.name" placeholder="如 GitLab" />
          </el-form-item>
          <el-form-item label="回调地址">
            <el-input v-model="createForm.redirect_uris" type="textarea" :rows="2" placeholder="每行一个，须与第三方系统配置的完全一致，如&#10;https://gitlab.example.com/users/auth/openid_connect/callback" />
          </el-form-item>
          <el-form-item label="认证方式">
            <el-select v-model="createForm.auth_method" style="width: 100%">
              <el-option label="client_secret_basic（推荐，服务端换码）" value="client_secret_basic" />
              <el-option label="client_secret_post" value="client_secret_post" />
              <el-option label="none（公共客户端，强制 PKCE）" value="none" />
            </el-select>
          </el-form-item>
          <el-form-item label="Scope">
            <el-input v-model="createForm.scope" />
          </el-form-item>
          <el-form-item label="入口地址">
            <el-input v-model="createForm.portal_url" placeholder="https://gitlab.example.com" />
          </el-form-item>
          <el-form-item label="描述">
            <el-input v-model="createForm.description" />
          </el-form-item>
          <el-form-item label="Logo 地址">
            <el-input v-model="createForm.logo_url" placeholder="https://…/logo.png（留空显示首字母占位）" />
          </el-form-item>
        </el-form>
      </template>
      <template v-else>
        <el-alert type="warning" :closable="false" style="margin-bottom: 12px">
          以下凭据仅显示一次，请立即复制到第三方系统配置中。
        </el-alert>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="client_id">{{ createdCreds.client_id }}</el-descriptions-item>
          <el-descriptions-item v-if="createdCreds.client_secret" label="client_secret">{{ createdCreds.client_secret }}</el-descriptions-item>
        </el-descriptions>
        <p class="dim" style="margin-top: 8px">
          第三方系统侧配置：Issuer 填本门户地址（自动发现端点），并使用上述 client_id / client_secret；回调地址须与注册值完全一致。
        </p>
      </template>
      <template #footer>
        <el-button @click="showCreate = false">关闭</el-button>
        <el-button v-if="!createdCreds" type="primary" :loading="creating" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showEdit" :title="`编辑客户端：${editing?.name || editing?.client_id || ''}`" width="520px">
      <el-form label-width="90px">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="应用名称（门户卡片标题）" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="应用描述（门户卡片展示）" />
        </el-form-item>
        <el-form-item label="Logo 地址">
          <el-input v-model="form.logo_url" placeholder="https://…/logo.png（留空显示首字母占位）" />
        </el-form-item>
        <el-form-item label="入口地址">
          <el-input v-model="form.portal_url" placeholder="点击卡片跳转的地址（不在卡片上展示）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showEdit = false">取消</el-button>
        <el-button type="primary" @click="saveEdit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
