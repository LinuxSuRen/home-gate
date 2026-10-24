<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getGroups, createGroup, deleteGroup } from '../api'

const groups = ref([])
const loading = ref(true)
const name = ref('')
const remark = ref('')
const builtin = ['admin', 'operate', 'view']

async function load() {
  loading.value = true
  try {
    groups.value = (await getGroups()) || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function add() {
  if (!name.value) {
    ElMessage.warning('组名不能为空')
    return
  }
  try {
    await createGroup({ name: name.value, remark: remark.value })
    name.value = ''
    remark.value = ''
    ElMessage.success('已创建')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function remove(g) {
  try {
    await ElMessageBox.confirm(`确定删除用户组「${g.name}」？`, '删除用户组', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteGroup(g.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<template>
  <div>
    <el-form inline style="margin-bottom: 12px">
      <el-form-item><el-input v-model="name" placeholder="组名" /></el-form-item>
      <el-form-item><el-input v-model="remark" placeholder="备注" /></el-form-item>
      <el-form-item><el-button type="primary" @click="add">新建用户组</el-button></el-form-item>
    </el-form>
    <el-table v-loading="loading" :data="groups">
      <el-table-column prop="name" label="组名" min-width="140" />
      <el-table-column prop="remark" label="备注" min-width="200" />
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button size="small" type="danger" :disabled="builtin.includes(row.name)" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <p class="dim">内置组 admin / operate / view 与各应用角色对应，映射关系在应用侧按客户端配置。</p>
  </div>
</template>
