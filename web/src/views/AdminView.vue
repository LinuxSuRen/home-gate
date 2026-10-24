<script setup>
import { ref } from 'vue'
import UsersView from './UsersView.vue'
import GroupsView from './GroupsView.vue'
import ClientsView from './ClientsView.vue'
import LinksView from './LinksView.vue'
import TokensView from './TokensView.vue'
import AuditView from './AuditView.vue'
import SettingsView from './SettingsView.vue'

const tab = ref('users')
</script>

<template>
  <div class="page" style="max-width: 1100px">
    <el-tabs v-model="tab">
      <el-tab-pane label="用户" name="users"><UsersView /></el-tab-pane>
      <el-tab-pane label="用户组" name="groups"><GroupsView /></el-tab-pane>
      <el-tab-pane label="客户端" name="clients"><ClientsView /></el-tab-pane>
      <el-tab-pane label="链接管理" name="links"><LinksView /></el-tab-pane>
      <el-tab-pane label="注册令牌" name="tokens"><TokensView /></el-tab-pane>
      <el-tab-pane label="审计日志" name="audit"><AuditView /></el-tab-pane>
      <el-tab-pane label="设置" name="settings"><SettingsView /></el-tab-pane>
    </el-tabs>
  </div>
</template>
