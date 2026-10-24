<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getUsers, getGroups, createUser, updateUser, deleteUser, resetPassword } from '../api'

const users = ref([])
const groups = ref([])
const loading = ref(true)
const showDlg = ref(false)
const editing = ref(null)
const form = ref({ username: '', name: '', email: '', groups: [], password: '' })
const showReset = ref(false)
const resetTarget = ref(null)
const newPassword = ref('')

const groupNames = computed(() => groups.value.map((g) => g.name))

async function loadAll() {
  loading.value = true
  try {
    ;[users.value, groups.value] = await Promise.all([getUsers(), getGroups()])
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(loadAll)

function openCreate() {
  editing.value = null
  form.value = { username: '', name: '', email: '', groups: [], password: '' }
  showDlg.value = true
}

function openEdit(u) {
  editing.value = u
  form.value = { username: u.username, name: u.name || '', email: u.email || '', groups: u.groups || [], password: '' }
  showDlg.value = true
}

async function save() {
  try {
    if (editing.value) {
      await updateUser(editing.value.id, { name: form.value.name, email: form.value.email, groups: form.value.groups })
    } else {
      if (!form.value.username || !form.value.password) {
        ElMessage.warning('用户名与密码必填')
        return
      }
      await createUser(form.value)
    }
    ElMessage.success('已保存')
    showDlg.value = false
    await loadAll()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function handleRemove(u) {
  try {
    await ElMessageBox.confirm(`确定删除用户「${u.username}」？该操作不可恢复。`, '删除用户', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteUser(u.id)
    ElMessage.success('已删除')
    await loadAll()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function openReset(u) {
  resetTarget.value = u
  newPassword.value = ''
  showReset.value = true
}

async function saveReset() {
  if (newPassword.value.length < 8) {
    ElMessage.warning('密码至少 8 位')
    return
  }
  try {
    const res = await resetPassword(resetTarget.value.id, newPassword.value)
    showReset.value = false
    await ElMessageBox.alert(`「${resetTarget.value.username}」的新密码：${res.password || newPassword.value}`, '重置成功', { confirmButtonText: '知道了' })
    await loadAll()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<template>
  <div>
    <div style="display: flex; justify-content: space-between; margin-bottom: 12px">
      <span class="dim">共 {{ users.length }} 个用户</span>
      <el-button type="primary" @click="openCreate">新建用户</el-button>
    </div>
    <el-table v-loading="loading" :data="users">
      <el-table-column prop="username" label="用户名" min-width="120" />
      <el-table-column prop="name" label="姓名" min-width="100" />
      <el-table-column prop="email" label="邮箱" min-width="160" />
      <el-table-column label="用户组" min-width="160">
        <template #default="{ row }">
          <el-tag v-for="g in row.groups" :key="g" size="small" style="margin-right: 4px">{{ g }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="来源" width="80">
        <template #default="{ row }">{{ row.feishu ? '飞书' : '本地' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag :type="row.active ? 'success' : 'danger'" size="small">{{ row.active ? '正常' : '禁用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="240" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" @click="openReset(row)">重置密码</el-button>
          <el-button size="small" type="danger" :disabled="row.username === 'admin'" @click="handleRemove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="showDlg" :title="editing ? `编辑用户 ${editing.username}` : '新建用户'" width="480px">
      <el-form label-width="80px">
        <el-form-item label="用户名">
          <el-input v-model="form.username" :disabled="!!editing" />
        </el-form-item>
        <el-form-item v-if="!editing" label="初始密码">
          <el-input v-model="form.password" type="password" show-password placeholder="至少 8 位，首次登录须修改" />
        </el-form-item>
        <el-form-item label="姓名"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="邮箱"><el-input v-model="form.email" /></el-form-item>
        <el-form-item label="用户组">
          <el-select v-model="form.groups" multiple placeholder="选择用户组" style="width: 100%">
            <el-option v-for="g in groupNames" :key="g" :label="g" :value="g" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDlg = false">取消</el-button>
        <el-button type="primary" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showReset" :title="`重置密码：${resetTarget?.username || ''}`" width="420px">
      <el-input v-model="newPassword" type="password" show-password placeholder="至少 8 位，重置后首次登录须修改" />
      <template #footer>
        <el-button @click="showReset = false">取消</el-button>
        <el-button type="primary" @click="saveReset">重置</el-button>
      </template>
    </el-dialog>
  </div>
</template>
