<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { getAudit } from '../api'

const logs = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const action = ref('')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await getAudit(page.value, pageSize.value, action.value)
    logs.value = res.data || []
    total.value = res.total || 0
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function changePage(p) {
  page.value = p
  load()
}

function fmt(t) {
  return t ? new Date(t).toLocaleString() : ''
}
</script>

<template>
  <div>
    <el-form inline style="margin-bottom: 12px">
      <el-form-item>
        <el-select v-model="action" placeholder="全部动作" clearable style="width: 200px" @change="page = 1; load()">
          <el-option v-for="a in ['login', 'login_failed', 'logout', 'authorize', 'token', 'dcr_register', 'dcr_update', 'dcr_delete', 'auto_provision', 'user_create', 'user_update', 'user_delete', 'user_reset_password', 'initial_token_create', 'initial_token_revoke', 'change_password', 'settings_update']" :key="a" :label="a" :value="a" />
        </el-select>
      </el-form-item>
    </el-form>
    <el-table v-loading="loading" :data="logs">
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ fmt(row.time || row.Time) }}</template>
      </el-table-column>
      <el-table-column prop="actor" label="操作者" width="120" />
      <el-table-column prop="action" label="动作" width="160" />
      <el-table-column prop="detail" label="详情" min-width="260" />
      <el-table-column prop="ip" label="来源 IP" width="140" />
    </el-table>
    <el-pagination
      style="margin-top: 12px; justify-content: flex-end"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="changePage"
    />
  </div>
</template>
