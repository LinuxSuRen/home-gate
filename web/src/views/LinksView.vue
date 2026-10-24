<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getLinks, createLink, updateLink, deleteLink } from '../api'

const links = ref([])
const loading = ref(true)
const showDlg = ref(false)
const editing = ref(null)
const form = ref({ name: '', url: '', description: '', logo_url: '', sort: 0, active: true })

async function loadAll() {
  loading.value = true
  try {
    links.value = (await getLinks()) || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(loadAll)

function openCreate() {
  editing.value = null
  form.value = { name: '', url: '', description: '', logo_url: '', sort: 0, active: true }
  showDlg.value = true
}

function openEdit(l) {
  editing.value = l
  form.value = {
    name: l.name || '', url: l.url || '', description: l.description || '',
    logo_url: l.logo_url || '', sort: l.sort || 0, active: l.active !== false,
  }
  showDlg.value = true
}

async function save() {
  if (!form.value.name || !form.value.url) {
    ElMessage.warning('名称与地址必填')
    return
  }
  try {
    if (editing.value) {
      await updateLink(editing.value.id, form.value)
    } else {
      await createLink(form.value)
    }
    ElMessage.success('已保存')
    showDlg.value = false
    await loadAll()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function handleRemove(l) {
  try {
    await ElMessageBox.confirm(`确定删除链接「${l.name}」？该操作不可恢复。`, '删除链接', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteLink(l.id)
    ElMessage.success('已删除')
    await loadAll()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<template>
  <div>
    <div style="display: flex; justify-content: space-between; margin-bottom: 12px">
      <span class="dim">共 {{ links.length }} 个链接（仅门户卡片跳转，不做认证聚合）</span>
      <el-button type="primary" @click="openCreate">新建链接</el-button>
    </div>
    <el-table v-loading="loading" :data="links">
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="url" label="地址" min-width="220" show-overflow-tooltip />
      <el-table-column prop="description" label="描述" min-width="160" show-overflow-tooltip />
      <el-table-column prop="sort" label="排序" width="80" />
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag :type="row.active ? 'success' : 'danger'" size="small">{{ row.active ? '启用' : '停用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" type="danger" @click="handleRemove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="showDlg" :title="editing ? `编辑链接 ${editing.name}` : '新建链接'" width="480px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="form.name" placeholder="必填" /></el-form-item>
        <el-form-item label="地址"><el-input v-model="form.url" placeholder="https://…（必填）" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="form.description" /></el-form-item>
        <el-form-item label="Logo"><el-input v-model="form.logo_url" placeholder="图片地址，留空显示首字母" /></el-form-item>
        <el-form-item label="排序"><el-input-number v-model="form.sort" :min="0" /></el-form-item>
        <el-form-item label="状态"><el-switch v-model="form.active" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDlg = false">取消</el-button>
        <el-button type="primary" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
