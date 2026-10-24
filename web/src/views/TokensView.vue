<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getInitialTokens, createInitialToken, revokeInitialToken } from '../api'

const tokens = ref([])
const loading = ref(true)
const remark = ref('')
const days = ref(7)

async function load() {
  loading.value = true
  try {
    tokens.value = (await getInitialTokens()) || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function issue() {
  try {
    const res = await createInitialToken(remark.value, days.value)
    await ElMessageBox.alert(
      `注册令牌（仅显示一次，交予待接入系统管理员）：\n${res.token}`,
      '签发成功',
      { confirmButtonText: '知道了' },
    )
    remark.value = ''
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function revoke(t) {
  try {
    await ElMessageBox.confirm('确定吊销该注册令牌？吊销后不能再用于 DCR 注册。', '吊销令牌', { type: 'warning' })
  } catch {
    return
  }
  try {
    await revokeInitialToken(t.id)
    ElMessage.success('已吊销')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function fmt(t) {
  return t ? new Date(t).toLocaleString() : ''
}
</script>

<template>
  <div>
    <p class="dim" style="margin-top: 0">注册令牌（initial access token）用于子系统通过 DCR 端点自助注册客户端。</p>
    <el-form inline style="margin-bottom: 12px">
      <el-form-item><el-input v-model="remark" placeholder="用途备注，如 demo-app 接入" style="width: 260px" /></el-form-item>
      <el-form-item>
        <el-input-number v-model="days" :min="1" :max="90" /> 天有效
      </el-form-item>
      <el-form-item><el-button type="primary" @click="issue">签发注册令牌</el-button></el-form-item>
    </el-form>
    <el-table v-loading="loading" :data="tokens">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="remark" label="备注" min-width="200" />
      <el-table-column label="过期时间" min-width="160">
        <template #default="{ row }">{{ fmt(row.expires_at) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.revoked ? 'danger' : 'success'" size="small">{{ row.revoked ? '已吊销' : '有效' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button size="small" type="danger" :disabled="row.revoked" @click="revoke(row)">吊销</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>
