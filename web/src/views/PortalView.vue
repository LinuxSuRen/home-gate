<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Star, StarFilled, Search } from '@element-plus/icons-vue'
import { getApps, followEntry, unfollowEntry } from '../api'

defineProps({ anonymous: Boolean })
const emit = defineEmits(['login'])

const cards = ref([])
const loading = ref(true)
const keyword = ref('')

// 搜索：按名称/描述/地址过滤（大小写不敏感）
const filteredCards = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  const list = kw
    ? cards.value.filter((c) =>
        [c.name, c.description, c.url].some((v) => (v || '').toLowerCase().includes(kw)))
    : cards.value
  return [...list].sort((a, b) => (b.followed ? 1 : 0) - (a.followed ? 1 : 0)) // 关注优先，组内稳定
})

onMounted(async () => {
  try {
    const data = (await getApps()) || {}
    const oauth = ((data.apps) || []).map((a) => ({
      key: `app-${a.client_id}`,
      kind: 'app',
      ref: a.client_id,
      name: a.name || a.client_id,
      description: a.description,
      logo_url: a.logo_url,
      url: a.portal_url,
      followed: !!a.followed,
    }))
    const links = ((data.links) || []).map((l) => ({
      key: `link-${l.id}`,
      kind: 'link',
      ref: String(l.id),
      name: l.name,
      description: l.description,
      logo_url: l.logo_url,
      url: l.url,
      followed: !!l.followed,
    }))
    cards.value = [...links, ...oauth]
  } catch {
    cards.value = []
  } finally {
    loading.value = false
  }
})

async function toggleFollow(card) {
  const target = cards.value.find((c) => c.key === card.key)
  if (!target) return
  try {
    if (target.followed) {
      await unfollowEntry(target.kind, target.ref)
      target.followed = false
    } else {
      await followEntry(target.kind, target.ref)
      target.followed = true
    }
  } catch (e) {
    ElMessage.error(e.message)
  }
}

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
    <div class="portal-head">
      <h2 style="margin: 0">应用中心</h2>
      <el-input
        v-model="keyword"
        :prefix-icon="Search"
        placeholder="搜索应用名称 / 描述 / 地址"
        clearable
        class="portal-search"
      />
    </div>
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
      v-else-if="filteredCards.length === 0"
      :description="keyword ? '没有匹配的应用' : (anonymous ? '暂无公开应用，登录后可查看更多' : '暂无已接入系统，可在管理后台通过 DCR 注册客户端并配置入口地址')"
    />
    <div v-else class="apps-grid">
      <a v-for="card in filteredCards" :key="card.key" :href="card.url" class="app-card" target="_blank">
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
        <el-tooltip v-if="!anonymous" :content="card.followed ? '取消关注' : '关注'" placement="top">
          <el-icon
            class="app-card__star"
            :class="{ 'is-followed': card.followed }"
            @click.stop.prevent="toggleFollow(card)"
          >
            <StarFilled v-if="card.followed" />
            <Star v-else />
          </el-icon>
        </el-tooltip>
      </a>
    </div>
  </div>
</template>

<style scoped>
.portal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.portal-search {
  max-width: 280px;
}
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
  position: relative;
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
.app-card__star {
  position: absolute;
  top: 10px;
  right: 10px;
  font-size: 16px;
  color: var(--color-text-dim);
  cursor: pointer;
}
.app-card__star:hover {
  color: var(--color-warning);
}
.app-card__star.is-followed {
  color: var(--color-warning);
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
