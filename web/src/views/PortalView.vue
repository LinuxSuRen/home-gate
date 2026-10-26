<script setup>
import { ref, onMounted } from 'vue'
import { getApps } from '../api'

defineProps({ anonymous: Boolean })
const emit = defineEmits(['login'])

const cards = ref([])
const loading = ref(true)

onMounted(async () => {
  try {
    const data = (await getApps()) || {}
    const oauth = ((data.apps) || []).map((a) => ({
      key: `app-${a.client_id}`,
      name: a.name || a.client_id,
      description: a.description,
      logo_url: a.logo_url,
      url: a.portal_url,
    }))
    const links = ((data.links) || []).map((l) => ({
      key: `link-${l.name}-${l.url}`,
      name: l.name,
      description: l.description,
      logo_url: l.logo_url,
      url: l.url,
    }))
    cards.value = [...links, ...oauth]
  } catch {
    cards.value = []
  } finally {
    loading.value = false
  }
})

function fallbackLogo(name) {
  return (name || '?').trim().charAt(0).toUpperCase()
}
function onLogoError(e) {
  e.target.style.display = 'none'
  const fallback = e.target.parentElement.querySelector('.app-card__logo-fallback')
  if (fallback) fallback.style.display = ''
}
</script>

<template>
  <div class="page">
    <h2 style="margin-top: 0">应用中心</h2>
    <el-alert
      v-if="anonymous"
      type="info"
      :closable="false"
      class="guest-banner"
    >
      <template #title>
        游客模式：仅展示公开应用，登录后可查看隐私链接等更多内容
        <el-button type="primary" size="small" class="guest-banner__btn" @click="emit('login')">立即登录</el-button>
      </template>
    </el-alert>
    <p v-if="loading" class="dim">正在加载…</p>
    <el-empty
      v-else-if="cards.length === 0"
      :description="anonymous ? '暂无公开应用，登录后可查看更多' : '暂无已接入系统，可在管理后台通过 DCR 注册客户端并配置入口地址'"
    />
    <div v-else class="apps-grid">
      <a v-for="card in cards" :key="card.key" :href="card.url" class="app-card" target="_blank">
        <div class="app-card__logo">
          <span class="app-card__logo-fallback" :style="card.logo_url ? 'display:none' : ''">{{
            fallbackLogo(card.name)
          }}</span>
          <img v-if="card.logo_url" :src="card.logo_url" :alt="card.name" @error="onLogoError" />
        </div>
        <div class="app-card__body">
          <div class="app-card__name">{{ card.name }}</div>
          <div class="app-card__desc">{{ card.description || ' ' }}</div>
        </div>
      </a>
    </div>
  </div>
</template>

<style scoped>
.guest-banner {
  margin-bottom: 16px;
}
.guest-banner__btn {
  margin-left: 12px;
}
.apps-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
}
.app-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px;
  background: var(--color-bg-panel);
  border: 1px solid var(--color-border-subtle);
  border-radius: 8px;
  text-decoration: none;
  color: var(--color-text-primary);
}
.app-card:hover {
  border-color: var(--color-accent);
}
.app-card__logo {
  width: 44px;
  height: 44px;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  overflow: hidden;
  background: var(--color-bg-base);
  border: 1px solid var(--color-border-subtle);
}
.app-card__logo img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.app-card__logo-fallback {
  font-size: 20px;
  font-weight: 600;
  color: var(--color-accent);
}
.app-card__body {
  min-width: 0;
}
.app-card__name {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 4px;
}
.app-card__desc {
  font-size: 12px;
  color: var(--color-text-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
</style>
