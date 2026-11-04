<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { login, feishuLoginURL, getFeishuEnabled, getVersion, getFaceEnabled, faceLogin } from '../api'

const props = defineProps({ next: { type: String, default: '/' } })
const emit = defineEmits(['success'])
const username = ref('')
const password = ref('')
const submitting = ref(false)
const feishuEnabled = ref(false)
const versionText = ref('')
const faceEnabled = ref(false)
const faceMode = ref(false)
const faceRunning = ref(false)
const faceHint = ref('')
const faceVideo = ref(null)
let faceStream = null
let faceTimer = null
let faceBusy = false

onMounted(async () => {
  try {
    feishuEnabled.value = await getFeishuEnabled()
  } catch {
    feishuEnabled.value = false
  }
  try {
    faceEnabled.value = await getFaceEnabled()
  } catch {
    faceEnabled.value = false
  }
  try {
    const v = await getVersion()
    versionText.value = v.version || ''
  } catch {
    versionText.value = ''
  }
})

onUnmounted(stopFace)

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

function toggleFace() {
  if (faceMode.value) {
    stopFace()
    faceMode.value = false
  } else {
    faceMode.value = true
    startFace()
  }
}

async function startFace() {
  if (faceRunning.value) return
  faceHint.value = '正在打开摄像头…'
  try {
    faceStream = await navigator.mediaDevices.getUserMedia({
      video: { width: { ideal: 1280 }, height: { ideal: 720 } },
      audio: false,
    })
  } catch {
    faceHint.value = '无法访问摄像头，请检查权限'
    return
  }
  faceRunning.value = true
  await nextTick()
  const el = faceVideo.value
  if (el) {
    el.srcObject = faceStream
    try {
      await el.play()
    } catch {}
  }
  faceHint.value = '正在识别…'
  faceTimer = setInterval(attemptFace, 2500)
}

function stopFace() {
  if (faceTimer) {
    clearInterval(faceTimer)
    faceTimer = null
  }
  if (faceStream) {
    faceStream.getTracks().forEach((t) => t.stop())
    faceStream = null
  }
  faceRunning.value = false
}

async function attemptFace() {
  const el = faceVideo.value
  if (!el || !el.videoWidth || faceBusy) return
  faceBusy = true
  const canvas = document.createElement('canvas')
  canvas.width = el.videoWidth
  canvas.height = el.videoHeight
  canvas.getContext('2d').drawImage(el, 0, 0)
  canvas.toBlob(async (blob) => {
    try {
      if (!blob) return
      const user = await faceLogin(blob)
      stopFace()
      emit('success', user)
    } catch (e) {
      faceHint.value = e.message
    } finally {
      faceBusy = false
    }
  }, 'image/jpeg', 0.85)
}
</script>

<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <div class="login-logo-wrap">
        <img src="/logo.svg" alt="logo" class="login-logo" />
      </div>
      <h2 class="login-title">Home Gate</h2>
      <p class="dim" style="margin-top: -8px">统一登录，一次登录访问所有系统</p>

      <template v-if="!faceMode">
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
      </template>

      <template v-else>
        <div class="face-cam">
          <video v-if="faceRunning" ref="faceVideo" playsinline muted></video>
          <div v-else class="face-off dim">{{ faceHint || '摄像头未启动' }}</div>
        </div>
        <p class="dim" style="font-size: 13px; text-align: center; margin: 8px 0">{{ faceHint }}</p>
        <el-button v-if="faceRunning" type="danger" size="large" style="width: 100%" @click="stopFace">
          停止识别
        </el-button>
      </template>

      <template v-if="faceEnabled">
        <el-divider>或</el-divider>
        <el-button size="large" style="width: 100%" @click="toggleFace">
          {{ faceMode ? '返回密码登录' : '使用人脸识别登录' }}
        </el-button>
      </template>

      <template v-if="feishuEnabled && !faceMode">
        <el-divider v-if="!faceEnabled">或</el-divider>
        <el-button size="large" style="width: 100%; margin-top: 8px" @click="goFeishu">
          使用飞书登录
        </el-button>
      </template>
    </el-card>
    <div class="login-version dim">home-gate {{ versionText }}</div>
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
.login-logo-wrap {
  text-align: center;
}
.login-logo {
  width: 56px;
  height: 56px;
}
.login-version {
  position: fixed;
  bottom: 16px;
}
.face-cam {
  width: 100%;
  aspect-ratio: 4 / 3;
  border-radius: 8px;
  overflow: hidden;
  background: #101827;
  display: flex;
  align-items: center;
  justify-content: center;
}
.face-cam video {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.face-off {
  padding: 40px;
  text-align: center;
}
</style>
