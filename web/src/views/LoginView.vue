<script setup>
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { login, feishuLoginURL } from '../api'

const props = defineProps({ next: { type: String, default: '/' } })
const emit = defineEmits(['success'])
const username = ref('')
const password = ref('')
const submitting = ref(false)

function goFeishu() {
  location.href = feishuLoginURL(props.next)
}

async function submit() {
  if (!username.value || !password.value) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  submitting.value = true
  try {
    const user = await login(username.value, password.value)
    emit('success', user)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <h2 class="login-title">Home Gate</h2>
      <p class="dim" style="margin-top: -8px">统一登录，一次登录访问所有系统</p>
      <el-form @submit.prevent="submit">
        <el-form-item>
          <el-input v-model="username" placeholder="用户名" size="large" @keyup.enter="submit" />
        </el-form-item>
        <el-form-item>
          <el-input v-model="password" type="password" placeholder="密码" size="large" show-password @keyup.enter="submit" />
        </el-form-item>
        <el-button type="primary" size="large" style="width: 100%" :loading="submitting" @click="submit">
          登录
        </el-button>
      </el-form>
      <el-divider>或</el-divider>
      <el-button size="large" style="width: 100%" @click="goFeishu">
        使用飞书登录
      </el-button>
    </el-card>
  </div>
</template>

<style scoped>
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-base);
}
.login-card {
  width: 380px;
}
.login-title {
  text-align: center;
}
</style>
