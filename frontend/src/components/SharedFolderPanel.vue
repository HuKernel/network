<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { request, upload } from '../lib/api'
import { formatBytes, formatTime } from '../lib/format'

type FolderItem = { id: string; name: string; mode: string; canUpload: boolean; canCreateFolder: boolean }
type BrowserItem = { name: string; itemType: string; path: string; size: number; updatedAt: string; canDelete: boolean }
type TrashItem = { id: string; originalName: string; originalPath: string; deletedAt: string; itemType: string }

const folders = ref<FolderItem[]>([])
const currentFolder = ref<FolderItem | null>(null)
const currentPath = ref('')
const items = ref<BrowserItem[]>([])
const trashItems = ref<TrashItem[]>([])
const loading = ref(false)
const createForm = reactive({ name: '', rootPath: '', mode: 'collaboration' })
const uploading = ref(false)
const uploadProgress = ref(0)

async function loadFolders() {
  const res = await request('/api/shared-folders')
  folders.value = res.folders || []
  if (!currentFolder.value && folders.value.length) await openFolder(folders.value[0])
}

async function loadTrash() {
  const res = await request('/api/admin/trash')
  trashItems.value = res.items || []
}

async function openFolder(folder: FolderItem, path = '') {
  currentFolder.value = folder
  currentPath.value = path
  const query = path ? `?path=${encodeURIComponent(path)}` : ''
  const res = await request(`/api/shared-folders/${folder.id}/items${query}`)
  items.value = res.items || []
}

async function createFolder() {
  try {
    await request('/api/admin/shared-folders', { method: 'POST', body: JSON.stringify(createForm) })
    Object.assign(createForm, { name: '', rootPath: '', mode: 'collaboration' })
    ElMessage.success('共享文件夹已创建')
    await loadFolders()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function uploadToFolder(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file || !currentFolder.value) return
  const form = new FormData()
  form.append('file', file)
  form.append('path', currentPath.value ? `${currentPath.value}/${file.name}` : file.name)
  uploading.value = true
  uploadProgress.value = 0
  try {
    await upload(`/api/shared-folders/${currentFolder.value.id}/upload`, form, (percent) => {
      uploadProgress.value = percent
    })
    ElMessage.success('上传成功')
    await openFolder(currentFolder.value, currentPath.value)
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    uploading.value = false
    target.value = ''
  }
}

function downloadItem(item: BrowserItem) {
  if (!currentFolder.value) return
  window.open(`/api/shared-folders/${currentFolder.value.id}/download?path=${encodeURIComponent(item.path)}`, '_blank')
}

async function createSubFolder() {
  if (!currentFolder.value) return
  try {
    const { value } = await ElMessageBox.prompt('输入子文件夹名称', '新建文件夹')
    await request(`/api/shared-folders/${currentFolder.value.id}/mkdir?path=${encodeURIComponent(currentPath.value)}`, {
      method: 'POST',
      body: JSON.stringify({ name: value }),
    })
    ElMessage.success('已创建')
    await openFolder(currentFolder.value, currentPath.value)
  } catch (error: any) {
    if (error?.message) ElMessage.error(error.message)
  }
}

async function deleteItem(item: BrowserItem) {
  if (!currentFolder.value) return
  try {
    await request(`/api/shared-folders/${currentFolder.value.id}/items?path=${encodeURIComponent(item.path)}`, { method: 'DELETE' })
    ElMessage.success('已移入回收站')
    await Promise.all([openFolder(currentFolder.value, currentPath.value), loadTrash()])
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function restoreTrash(id: string) {
  try {
    await request(`/api/admin/trash/${id}/restore`, { method: 'POST' })
    ElMessage.success('已恢复')
    await Promise.all([loadTrash(), currentFolder.value ? openFolder(currentFolder.value, currentPath.value) : Promise.resolve()])
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function purgeTrash(id: string) {
  try {
    await request(`/api/admin/trash/${id}/purge`, { method: 'DELETE' })
    ElMessage.success('已彻底删除')
    await loadTrash()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

function goParent() {
  if (!currentFolder.value || !currentPath.value) return
  const parent = currentPath.value.split('/').slice(0, -1).join('/')
  void openFolder(currentFolder.value, parent)
}

onMounted(async () => {
  loading.value = true
  try {
    await Promise.all([loadFolders(), loadTrash()])
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="shared-folder-panel">
    <el-card class="section">
      <template #header>共享文件夹</template>
      <div class="toolbar">
        <el-input v-model="createForm.name" placeholder="名称" />
        <el-select v-model="createForm.mode">
          <el-option label="协作" value="collaboration" />
          <el-option label="只读" value="readonly" />
          <el-option label="上传箱" value="upload_box" />
        </el-select>
        <el-input v-model="createForm.rootPath" placeholder="物理路径，留空自动生成" />
        <el-button type="primary" @click="createFolder">创建</el-button>
      </div>
      <el-table :data="folders" v-loading="loading" @row-click="openFolder">
        <el-table-column prop="name" label="名称" min-width="180" />
        <el-table-column prop="mode" label="模式" width="140" />
        <el-table-column label="操作" width="120">
          <template #default="{ row }">
            <el-button link type="primary" @click.stop="openFolder(row)">浏览</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card class="section" v-if="currentFolder">
      <template #header>{{ currentFolder.name }} / {{ currentPath || '/' }}</template>
      <div class="toolbar">
        <el-button :disabled="!currentPath" @click="goParent">返回上级</el-button>
        <label class="upload-button" :class="{ 'is-disabled': uploading }" v-if="currentFolder.canUpload">
          <input type="file" :disabled="uploading" @change="uploadToFolder" />
          <span>{{ uploading ? `上传中 ${uploadProgress}%` : '上传文件' }}</span>
        </label>
        <el-button v-if="currentFolder.canCreateFolder" @click="createSubFolder">新建文件夹</el-button>
      </div>
      <el-progress v-if="uploading" :percentage="uploadProgress" class="upload-progress" />
      <el-table :data="items">
        <el-table-column prop="name" label="名称" min-width="220">
          <template #default="{ row }">
            <el-button v-if="row.itemType === 'folder'" link type="primary" @click="openFolder(currentFolder!, row.path)">{{ row.name }}</el-button>
            <span v-else>{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="itemType" label="类型" width="100" />
        <el-table-column label="大小" width="120" :formatter="(row: any) => (row.itemType === 'file' ? formatBytes(row.size) : '-')" />
        <el-table-column label="更新时间" min-width="180" :formatter="(row: any) => formatTime(row.updatedAt)" />
        <el-table-column label="操作" width="180">
          <template #default="{ row }">
            <el-button v-if="row.itemType === 'file'" link type="primary" @click="downloadItem(row)">下载</el-button>
            <el-button v-if="row.canDelete" link type="danger" @click="deleteItem(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card class="section">
      <template #header>回收站</template>
      <el-table :data="trashItems">
        <el-table-column prop="originalName" label="文件名" min-width="180" />
        <el-table-column prop="originalPath" label="原路径" min-width="220" />
        <el-table-column label="删除时间" min-width="180" :formatter="(row: any) => formatTime(row.deletedAt)" />
        <el-table-column label="操作" width="180">
          <template #default="{ row }">
            <el-button link type="primary" @click="restoreTrash(row.id)">恢复</el-button>
            <el-button link type="danger" @click="purgeTrash(row.id)">彻底删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.shared-folder-panel {
  display: grid;
  gap: 16px;
}
.toolbar {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
.section {
  border-radius: 16px;
}
.upload-button {
  position: relative;
  overflow: hidden;
  display: inline-flex;
  align-items: center;
  padding: 8px 14px;
  border-radius: 10px;
  background: #409eff;
  color: #fff;
  cursor: pointer;
}
.upload-button input {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
}
.upload-button.is-disabled {
  opacity: 0.6;
  pointer-events: none;
}
.upload-progress {
  margin: 8px 0 4px;
}
</style>
