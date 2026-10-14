<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import UserPermissionDialog from './components/UserPermissionDialog.vue'
import SharedFolderPanel from './components/SharedFolderPanel.vue'
import { request, upload } from './lib/api'
import { formatBytes, formatTime } from './lib/format'
type User = {
  id: string
  username: string
  displayName: string
  roleId: string
  canUpload: boolean
  canDownload: boolean
}
type UserItem = {
  id: string
  username: string
  displayName: string
  roleId: string
  status: string
  canUpload: boolean
  canDownload: boolean
  createdAt: string
  lastLoginAt: string
}
type RoleItem = {
  id: string
  name: string
  description: string
  isSystem: boolean
  canUpload: boolean
  canDownload: boolean
}
type DeviceItem = {
  id: string
  deviceName: string
  status: string
  lastIP: string
}
type ShareItem = {
  id: string
  fileName: string
  visibility: string
  size: number
  createdAt: string
  expiresAt: string
  maxDownloads: number
  downloadCount: number
  requireFilePassword: boolean
}
const loading = ref(false)
const initialized = ref(false)
const me = ref<User | null>(null)
const users = ref<UserItem[]>([])
const files = ref<any[]>([])
const shares = ref<ShareItem[]>([])
const devices = ref<DeviceItem[]>([])
const ipRules = ref<any[]>([])
const transfers = ref<any[]>([])
const logs = ref<any[]>([])
const roles = ref<RoleItem[]>([])
const latestBatch = ref<{ username: string; initialPassword: string }[]>([])
const activeAdminTab = ref('users')
const setupForm = reactive({
  username: 'admin',
  displayName: '系统管理员',
  password: '',
  confirmPassword: '',
})
const loginForm = reactive({
  username: '',
  password: '',
  deviceName: navigator.platform || 'Browser',
})
const createUserForm = reactive({
  username: '',
  displayName: '',
  password: '',
  roleId: 'user',
  canUpload: true,
  canDownload: true,
  status: 'active',
  expiresAt: '',
  remark: '',
})
const batchForm = reactive({
  prefix: 'student',
  startNumber: 1,
  count: 10,
  numberWidth: 3,
  roleId: 'user',
  defaultStatus: 'active',
})
const ipForm = reactive({
  ruleType: 'deny',
  ipValue: '',
  reason: '',
})
const roleForm = reactive({
  id: '',
  name: '',
  description: '',
  canUpload: true,
  canDownload: true,
})
const userBindingForm = reactive({
  userId: '',
  username: '',
  allowedIPsText: '',
  allowedDeviceIds: [] as string[],
})
const importForm = reactive({
  file: null as File | null,
  conflictStrategy: 'skip',
  defaultRoleId: 'user',
  defaultStatus: 'active',
})
const exportForm = reactive({
  format: 'csv',
  roleId: '',
  status: '',
})
const shareForm = reactive({
  fileId: '',
  visibility: 'all_users',
  allowedUserIdsText: '',
  allowedIPsText: '',
  allowedRoleIds: [] as string[],
  allowedDeviceIds: [] as string[],
  requireFilePassword: false,
  filePassword: '',
  expiresAt: '',
  maxDownloads: 0,
})
const createUserDialog = ref(false)
const batchDialog = ref(false)
const ipDialog = ref(false)
const shareDialog = ref(false)
const roleDialog = ref(false)
const bindingDialog = ref(false)
const importDialog = ref(false)
const exportDialog = ref(false)
const permissionDialogOpen = ref(false), permissionDialogUser = ref<UserItem | null>(null)
const selectedUsers = ref<UserItem[]>([]), isAdmin = computed(() => me.value?.roleId === 'admin')
const uploading = ref(false)
const uploadProgress = ref(0)

async function bootstrap() {
  loading.value = true
  try {
    const setup = await request('/api/setup/status')
    initialized.value = setup.initialized
    if (initialized.value) {
      try {
        const current = await request('/api/auth/me')
        me.value = current.user
        await refreshData()
      } catch {
        me.value = null
      }
    }
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}
async function refreshData() {
  if (!me.value) return
  const [fileRes, shareRes] = await Promise.all([request('/api/files'), request('/api/shares')])
  files.value = fileRes.files || []
  shares.value = shareRes.shares || []
  if (isAdmin.value) {
    const [userRes, deviceRes, ipRes, transferRes, logRes, roleRes] = await Promise.all([request('/api/admin/users'), request('/api/admin/devices'), request('/api/admin/ip-rules'), request('/api/admin/transfers'), request('/api/admin/audit-logs'), request('/api/admin/roles')])
    users.value = userRes.users || []
    devices.value = deviceRes.devices || []
    ipRules.value = ipRes.rules || []
    transfers.value = transferRes.transfers || []
    logs.value = logRes.logs || []
    roles.value = roleRes.roles || []
  }
}
async function setupAdmin() {
  if (setupForm.password !== setupForm.confirmPassword) {
    ElMessage.error('两次密码不一致')
    return
  }
  loading.value = true
  try {
    await request('/api/setup/admin', {
      method: 'POST',
      body: JSON.stringify({
        username: setupForm.username,
        displayName: setupForm.displayName,
        password: setupForm.password,
      }),
    })
    initialized.value = true
    ElMessage.success('管理员创建成功，请登录')
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}
async function login() {
  loading.value = true
  try {
    await request('/api/auth/login', { method: 'POST', body: JSON.stringify(loginForm) })
    const current = await request('/api/auth/me')
    me.value = current.user
    await refreshData()
    ElMessage.success('登录成功')
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    loading.value = false
  }
}
async function logout() {
  await request('/api/auth/logout', { method: 'POST' })
  me.value = null
  users.value = []
  files.value = []
  shares.value = []
}
async function createUser() {
  try {
    const res = await request('/api/admin/users', { method: 'POST', body: JSON.stringify(createUserForm) })
    ElMessage.success(res.initialPassword ? `创建成功，初始密码：${res.initialPassword}` : '创建成功')
    createUserDialog.value = false
    Object.assign(createUserForm, {
      username: '',
      displayName: '',
      password: '',
      roleId: 'user',
      canUpload: true,
      canDownload: true,
      status: 'active',
      expiresAt: '',
      remark: '',
    })
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
function applyRoleTemplate(roleID: string, target: { canUpload: boolean; canDownload: boolean }) {
  const role = roles.value.find((item) => item.id === roleID)
  if (!role) return
  Object.assign(target, { canUpload: role.canUpload, canDownload: role.canDownload })
}
async function batchGenerate() {
  try {
    const res = await request('/api/admin/users/batch-generate', { method: 'POST', body: JSON.stringify(batchForm) })
    latestBatch.value = res.users || []
    batchDialog.value = false
    ElMessage.success(`已生成 ${res.createdCount} 个账号`)
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function toggleUser(user: UserItem, action: 'enable' | 'disable') {
  try {
    await request(`/api/admin/users/${user.id}/${action}`, { method: 'POST' })
    ElMessage.success('操作成功')
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function resetPassword(user: UserItem) {
  try {
    const res = await request(`/api/admin/users/${user.id}/reset-password`, { method: 'POST', body: JSON.stringify({}) })
    await ElMessageBox.alert(`用户：${user.username}\n新密码：${res.initialPassword}`, '密码已重置')
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function runBatchAction(action: 'enable' | 'disable' | 'reset_password') {
  if (!selectedUsers.value.length) {
    ElMessage.warning('请先选择用户')
    return
  }
  try {
    const res = await request('/api/admin/users/batch-action', { method: 'POST', body: JSON.stringify({ userIds: selectedUsers.value.map((user) => user.id), action }) })
    if (action === 'reset_password' && res.passwords?.length) {
      await ElMessageBox.alert(res.passwords.map((item: any) => `${item.username}: ${item.initialPassword}`).join('\n'), '批量重置结果')
    }
    ElMessage.success(`已处理 ${res.affected} 个用户${res.skipped ? `，跳过 ${res.skipped} 个` : ''}`)
    selectedUsers.value = []
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function addIPRule() {
  try {
    await request('/api/admin/ip-rules', { method: 'POST', body: JSON.stringify(ipForm) })
    ipDialog.value = false
    Object.assign(ipForm, { ruleType: 'deny', ipValue: '', reason: '' })
    await refreshData()
    ElMessage.success('IP 规则已添加')
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function blockDevice(id: string) {
  try {
    await request(`/api/admin/devices/${id}/block`, { method: 'POST' })
    ElMessage.success('设备已拉黑')
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function uploadFile(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file) return
  const form = new FormData()
  form.append('file', file)
  uploading.value = true
  uploadProgress.value = 0
  try {
    await upload('/api/files/upload', form, (percent) => {
      uploadProgress.value = percent
    })
    ElMessage.success('上传成功')
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  } finally {
    uploading.value = false
    target.value = ''
  }
}
async function createShare() {
  try {
    await request('/api/shares', {
      method: 'POST',
      body: JSON.stringify({
        fileId: shareForm.fileId,
        visibility: shareForm.visibility,
        allowedUserIds: splitTextList(shareForm.allowedUserIdsText),
        allowedIPs: splitTextList(shareForm.allowedIPsText),
        allowedRoleIds: shareForm.allowedRoleIds,
        allowedDeviceIds: shareForm.allowedDeviceIds,
        requireFilePassword: shareForm.requireFilePassword,
        filePassword: shareForm.filePassword,
        expiresAt: shareForm.expiresAt,
        maxDownloads: shareForm.maxDownloads,
      }),
    })
    shareDialog.value = false
    resetShareForm()
    ElMessage.success('共享已创建')
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function downloadShare(share: ShareItem) {
  let url = `/api/shares/${share.id}/download`
  if (share.requireFilePassword) {
    try {
      const result = await ElMessageBox.prompt('请输入文件密码', '文件受保护', {
        inputType: 'password',
        confirmButtonText: '下载',
        cancelButtonText: '取消',
      })
      url += `?password=${encodeURIComponent(result.value)}`
    } catch {
      return
    }
  }
  window.open(url, '_blank')
}
function exportLatestBatchFile() {
  window.open('/api/admin/users/export-latest-batch', '_blank')
}
function openRoleDialog(role?: RoleItem) {
  if (role) {
    Object.assign(roleForm, {
      id: role.id,
      name: role.name,
      description: role.description,
      canUpload: role.canUpload,
      canDownload: role.canDownload,
    })
  } else {
    Object.assign(roleForm, { id: '', name: '', description: '', canUpload: true, canDownload: true })
  }
  roleDialog.value = true
}
async function saveRole() {
  try {
    const method = roleForm.id ? 'PUT' : 'POST'
    const path = roleForm.id ? `/api/admin/roles/${roleForm.id}` : '/api/admin/roles'
    await request(path, {
      method,
      body: JSON.stringify({
        name: roleForm.name,
        description: roleForm.description,
        canUpload: roleForm.canUpload,
        canDownload: roleForm.canDownload,
      }),
    })
    roleDialog.value = false
    await refreshData()
    ElMessage.success('角色模板已保存')
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function removeRole(role: RoleItem) {
  try {
    await request(`/api/admin/roles/${role.id}`, { method: 'DELETE' })
    await refreshData()
    ElMessage.success('角色模板已删除')
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
async function openBindingDialog(user: UserItem) {
  try {
    const res = await request(`/api/admin/users/${user.id}/bindings`)
    Object.assign(userBindingForm, {
      userId: user.id,
      username: user.username,
      allowedIPsText: (res.allowedIPs || []).join('\n'),
      allowedDeviceIds: res.allowedDeviceIds || [],
    })
    bindingDialog.value = true
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
function openPermissionDialog(user: UserItem) {
  permissionDialogUser.value = user
  permissionDialogOpen.value = true
}
async function saveBindings() {
  try {
    await request(`/api/admin/users/${userBindingForm.userId}/bindings`, {
      method: 'PUT',
      body: JSON.stringify({
        allowedIPs: splitTextList(userBindingForm.allowedIPsText),
        allowedDeviceIds: userBindingForm.allowedDeviceIds,
      }),
    })
    bindingDialog.value = false
    ElMessage.success('账号绑定已更新')
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
function onImportFileChange(event: Event) {
  const target = event.target as HTMLInputElement
  importForm.file = target.files?.[0] || null
}
async function importUsers() {
  if (!importForm.file) {
    ElMessage.error('请先选择文件')
    return
  }
  const form = new FormData()
  form.append('file', importForm.file)
  form.append('conflictStrategy', importForm.conflictStrategy)
  form.append('defaultRoleId', importForm.defaultRoleId)
  form.append('defaultStatus', importForm.defaultStatus)
  try {
    const res = await request('/api/admin/users/import', { method: 'POST', body: form })
    importDialog.value = false
    importForm.file = null
    ElMessage.success(`导入完成：成功 ${res.imported}，跳过 ${res.skipped}`)
    await refreshData()
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}
function openExport() {
  const params = new URLSearchParams()
  params.set('format', exportForm.format)
  if (exportForm.roleId) params.set('roleId', exportForm.roleId)
  if (exportForm.status) params.set('status', exportForm.status)
  window.open(`/api/admin/users/export?${params.toString()}`, '_blank')
}

function splitTextList(value: string) {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function resetShareForm() {
  Object.assign(shareForm, {
    fileId: '',
    visibility: 'all_users',
    allowedUserIdsText: '',
    allowedIPsText: '',
    allowedRoleIds: [],
    allowedDeviceIds: [],
    requireFilePassword: false,
    filePassword: '',
    expiresAt: '',
    maxDownloads: 0,
  })
}

onMounted(bootstrap)
</script>

<template>
  <div class="shell">
    <el-card v-if="loading && !initialized" class="panel">
      <el-skeleton :rows="6" animated />
    </el-card>

    <el-card v-else-if="!initialized" class="panel">
      <template #header>
        <div class="panel-header">
          <div>
            <h1>局域网文件传输系统</h1>
            <p>首次启动请创建管理员账号</p>
          </div>
        </div>
      </template>
      <el-form label-position="top">
        <el-form-item label="管理员用户名">
          <el-input v-model="setupForm.username" />
        </el-form-item>
        <el-form-item label="显示名称">
          <el-input v-model="setupForm.displayName" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="setupForm.password" show-password type="password" />
        </el-form-item>
        <el-form-item label="确认密码">
          <el-input v-model="setupForm.confirmPassword" show-password type="password" />
        </el-form-item>
        <el-button type="primary" @click="setupAdmin">创建管理员</el-button>
      </el-form>
    </el-card>

    <el-card v-else-if="!me" class="panel">
      <template #header>
        <div class="panel-header">
          <div>
            <h1>局域网文件传输系统</h1>
            <p>账号由管理员分配，不支持自行注册</p>
          </div>
        </div>
      </template>
      <el-form label-position="top">
        <el-form-item label="用户名">
          <el-input v-model="loginForm.username" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="loginForm.password" type="password" show-password />
        </el-form-item>
        <el-form-item label="设备名称">
          <el-input v-model="loginForm.deviceName" />
        </el-form-item>
        <el-button type="primary" :loading="loading" @click="login">登录</el-button>
      </el-form>
    </el-card>

    <template v-else>
      <header class="topbar">
        <div>
          <h1>局域网安全文件传输</h1>
          <p>{{ me.displayName }}（{{ me.username }}）</p>
        </div>
        <div class="topbar-actions">
          <el-tag type="success">{{ me.roleId === 'admin' ? '管理员' : '普通用户' }}</el-tag>
          <el-button @click="refreshData">刷新</el-button>
          <el-button type="danger" plain @click="logout">退出</el-button>
        </div>
      </header>

      <main v-if="isAdmin" class="admin-layout">
        <aside class="summary-grid">
          <el-card><strong>{{ users.length }}</strong><span>用户数</span></el-card>
          <el-card><strong>{{ roles.length }}</strong><span>角色模板</span></el-card>
          <el-card><strong>{{ shares.length }}</strong><span>共享数</span></el-card>
          <el-card><strong>{{ devices.length }}</strong><span>设备数</span></el-card>
        </aside>

        <el-tabs v-model="activeAdminTab" class="tabs">
          <el-tab-pane label="用户管理" name="users">
            <div class="toolbar">
              <el-button type="primary" @click="createUserDialog = true">新增用户</el-button>
              <el-button @click="batchDialog = true">批量生成</el-button>
              <el-button :disabled="!selectedUsers.length" @click="runBatchAction('enable')">批量启用</el-button>
              <el-button :disabled="!selectedUsers.length" @click="runBatchAction('disable')">批量禁用</el-button>
              <el-button :disabled="!selectedUsers.length" @click="runBatchAction('reset_password')">批量重置密码</el-button>
              <el-button @click="importDialog = true">导入用户</el-button>
              <el-button @click="exportDialog = true">导出用户</el-button>
              <el-button @click="exportLatestBatchFile">导出最近批次</el-button>
            </div>
            <el-table :data="users" row-key="id" @selection-change="selectedUsers = $event">
              <el-table-column type="selection" :selectable="(row: UserItem) => row.roleId !== 'admin'" width="48" />
              <el-table-column prop="username" label="用户名" min-width="140" />
              <el-table-column prop="displayName" label="显示名" min-width="140" />
              <el-table-column prop="roleId" label="角色" width="140" />
              <el-table-column prop="status" label="状态" width="100" />
              <el-table-column label="权限" min-width="180">
                <template #default="{ row }">
                  <el-tag size="small" :type="row.canUpload ? 'success' : 'info'">上传 {{ row.canUpload ? '开' : '关' }}</el-tag>
                  <el-tag size="small" :type="row.canDownload ? 'warning' : 'info'">下载 {{ row.canDownload ? '开' : '关' }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="240">
                <template #default="{ row }">
                  <el-button link type="primary" :disabled="row.roleId === 'admin'" @click="openPermissionDialog(row)">权限</el-button>
                  <el-button link type="warning" @click="resetPassword(row)">重置密码</el-button>
                  <el-button link type="primary" @click="openBindingDialog(row)">绑定</el-button>
                  <el-button v-if="row.status === 'active'" link type="danger" @click="toggleUser(row, 'disable')">禁用</el-button>
                  <el-button v-else link type="primary" @click="toggleUser(row, 'enable')">启用</el-button>
                </template>
              </el-table-column>
            </el-table>
            <el-card v-if="latestBatch.length" class="batch-card">
              <template #header>最近一次批量生成</template>
              <el-table :data="latestBatch">
                <el-table-column prop="username" label="用户名" />
                <el-table-column prop="initialPassword" label="初始密码" />
              </el-table>
            </el-card>
          </el-tab-pane>

          <el-tab-pane label="角色模板" name="roles">
            <div class="toolbar">
              <el-button type="primary" @click="openRoleDialog()">新增角色模板</el-button>
            </div>
            <el-table :data="roles">
              <el-table-column prop="name" label="名称" min-width="180" />
              <el-table-column prop="description" label="说明" min-width="220" />
              <el-table-column label="默认权限" min-width="180">
                <template #default="{ row }">
                  <el-tag size="small" :type="row.canUpload ? 'success' : 'info'">上传 {{ row.canUpload ? '开' : '关' }}</el-tag>
                  <el-tag size="small" :type="row.canDownload ? 'warning' : 'info'">下载 {{ row.canDownload ? '开' : '关' }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="180">
                <template #default="{ row }">
                  <el-button link type="primary" @click="openRoleDialog(row)">编辑</el-button>
                  <el-button v-if="!row.isSystem" link type="danger" @click="removeRole(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane label="文件与共享" name="files">
            <div class="toolbar">
              <label class="upload-button" :class="{ 'is-disabled': uploading }">
                <input type="file" :disabled="uploading" @change="uploadFile" />
                <span>{{ uploading ? `上传中 ${uploadProgress}%` : '上传文件' }}</span>
              </label>
              <el-button @click="shareDialog = true">创建共享</el-button>
            </div>
            <el-progress v-if="uploading" :percentage="uploadProgress" class="upload-progress" />
            <SharedFolderPanel />
            <el-table :data="files">
              <el-table-column prop="originalName" label="文件名" min-width="220" />
              <el-table-column label="大小" width="160" :formatter="(row: any) => formatBytes(row.size)" />
              <el-table-column label="上传时间" min-width="220" :formatter="(row: any) => formatTime(row.createdAt)" />
            </el-table>
            <el-divider />
            <el-table :data="shares">
              <el-table-column prop="fileName" label="共享文件" min-width="220" />
              <el-table-column prop="visibility" label="可见范围" width="160" />
              <el-table-column prop="expiresAt" label="有效期" min-width="180" />
              <el-table-column label="下载限制" min-width="150">
                <template #default="{ row }">
                  <span v-if="row.maxDownloads > 0">{{ row.downloadCount }}/{{ row.maxDownloads }}</span>
                  <span v-else>不限</span>
                </template>
              </el-table-column>
              <el-table-column label="密码" width="100">
                <template #default="{ row }">
                  <el-tag v-if="row.requireFilePassword" type="danger">需要</el-tag>
                  <el-tag v-else type="info">无</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane label="IP 与设备" name="security">
            <div class="split-grid">
              <el-card>
                <template #header>
                  <div class="card-header">
                    <span>IP 黑名单</span>
                    <el-button type="primary" link @click="ipDialog = true">新增</el-button>
                  </div>
                </template>
                <el-table :data="ipRules">
                  <el-table-column prop="ipValue" label="IP" />
                  <el-table-column prop="reason" label="原因" />
                </el-table>
              </el-card>
              <el-card>
                <template #header>设备列表</template>
                <el-table :data="devices">
                  <el-table-column prop="deviceName" label="设备名" />
                  <el-table-column prop="status" label="状态" />
                  <el-table-column prop="lastIP" label="最近 IP" />
                  <el-table-column label="操作" width="120">
                    <template #default="{ row }">
                      <el-button link type="danger" @click="blockDevice(row.id)">拉黑</el-button>
                    </template>
                  </el-table-column>
                </el-table>
              </el-card>
            </div>
          </el-tab-pane>

          <el-tab-pane label="日志" name="logs">
            <div class="split-grid">
              <el-card>
                <template #header>传输记录</template>
                <el-table :data="transfers">
                  <el-table-column prop="direction" label="方向" width="100" />
                  <el-table-column label="大小" width="140" :formatter="(row: any) => formatBytes(row.bytesTotal)" />
                  <el-table-column prop="ip" label="IP" width="140" />
                  <el-table-column label="时间" min-width="220" :formatter="(row: any) => formatTime(row.createdAt)" />
                </el-table>
              </el-card>
              <el-card>
                <template #header>安全日志</template>
                <el-table :data="logs">
                  <el-table-column prop="eventType" label="事件" width="160" />
                  <el-table-column prop="message" label="说明" min-width="220" />
                  <el-table-column prop="createdAt" label="时间" min-width="220" />
                </el-table>
              </el-card>
            </div>
          </el-tab-pane>
        </el-tabs>
      </main>

      <main v-else class="user-layout">
        <el-card class="panel wide">
          <template #header>
            <div class="card-header">
              <span>我的文件</span>
              <label v-if="me.canUpload" class="upload-button" :class="{ 'is-disabled': uploading }">
                <input type="file" :disabled="uploading" @change="uploadFile" />
                <span>{{ uploading ? `上传中 ${uploadProgress}%` : '上传文件' }}</span>
              </label>
            </div>
          </template>
          <el-progress v-if="uploading" :percentage="uploadProgress" class="upload-progress" />
          <el-empty v-if="!files.length" description="还没有上传文件" />
          <el-table v-else :data="files">
            <el-table-column prop="originalName" label="文件名" min-width="220" />
            <el-table-column label="大小" width="160" :formatter="(row: any) => formatBytes(row.size)" />
            <el-table-column label="上传时间" min-width="220" :formatter="(row: any) => formatTime(row.createdAt)" />
          </el-table>
        </el-card>

        <el-card class="panel wide">
          <template #header>可下载共享</template>
          <el-empty v-if="!shares.length" description="暂无可访问共享文件" />
          <el-table v-else :data="shares">
            <el-table-column prop="fileName" label="文件名" min-width="220" />
            <el-table-column prop="visibility" label="权限范围" width="160" />
            <el-table-column prop="expiresAt" label="有效期" min-width="180" />
            <el-table-column label="操作" width="140">
              <template #default="{ row }">
                <el-button link type="primary" @click="downloadShare(row)">下载</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </main>
    </template>

    <el-dialog v-model="createUserDialog" title="新增用户" width="520px">
      <el-form label-position="top">
        <el-form-item label="用户名"><el-input v-model="createUserForm.username" /></el-form-item>
        <el-form-item label="显示名"><el-input v-model="createUserForm.displayName" /></el-form-item>
        <el-form-item label="初始密码"><el-input v-model="createUserForm.password" placeholder="留空则随机生成" /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="createUserForm.roleId" @change="applyRoleTemplate(createUserForm.roleId, createUserForm)">
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="权限">
          <el-switch v-model="createUserForm.canUpload" active-text="允许上传" />
          <el-switch v-model="createUserForm.canDownload" active-text="允许下载" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createUserDialog = false">取消</el-button>
        <el-button type="primary" @click="createUser">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="batchDialog" title="批量生成账号" width="520px">
      <el-form label-position="top">
        <el-form-item label="账号前缀"><el-input v-model="batchForm.prefix" /></el-form-item>
        <el-form-item label="起始编号"><el-input-number v-model="batchForm.startNumber" :min="1" /></el-form-item>
        <el-form-item label="数量"><el-input-number v-model="batchForm.count" :min="1" :max="200" /></el-form-item>
        <el-form-item label="数字位数"><el-input-number v-model="batchForm.numberWidth" :min="1" :max="6" /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="batchForm.roleId">
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="batchDialog = false">取消</el-button>
        <el-button type="primary" @click="batchGenerate">生成</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="ipDialog" title="新增 IP 黑名单" width="480px">
      <el-form label-position="top">
        <el-form-item label="规则类型">
          <el-select v-model="ipForm.ruleType">
            <el-option label="拒绝" value="deny" />
          </el-select>
        </el-form-item>
        <el-form-item label="IP 地址"><el-input v-model="ipForm.ipValue" placeholder="例如 192.168.1.52" /></el-form-item>
        <el-form-item label="原因"><el-input v-model="ipForm.reason" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="ipDialog = false">取消</el-button>
        <el-button type="primary" @click="addIPRule">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="roleDialog" :title="roleForm.id ? '编辑角色模板' : '新增角色模板'" width="520px">
      <el-form label-position="top">
        <el-form-item label="名称"><el-input v-model="roleForm.name" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="roleForm.description" /></el-form-item>
        <el-form-item label="默认权限">
          <el-switch v-model="roleForm.canUpload" active-text="允许上传" />
          <el-switch v-model="roleForm.canDownload" active-text="允许下载" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="roleDialog = false">取消</el-button>
        <el-button type="primary" @click="saveRole">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="bindingDialog" title="账号绑定" width="560px">
      <el-form label-position="top">
        <el-form-item label="用户">
          <el-input :model-value="userBindingForm.username" disabled />
        </el-form-item>
        <el-form-item label="允许登录的 IP">
          <el-input
            v-model="userBindingForm.allowedIPsText"
            type="textarea"
            :rows="5"
            placeholder="每行一个 IP，留空表示不限制"
          />
        </el-form-item>
        <el-form-item label="允许登录的设备">
          <el-select v-model="userBindingForm.allowedDeviceIds" multiple clearable>
            <el-option v-for="device in devices" :key="device.id" :label="`${device.deviceName} (${device.id.slice(0, 8)})`" :value="device.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="bindingDialog = false">取消</el-button>
        <el-button type="primary" @click="saveBindings">保存</el-button>
      </template>
    </el-dialog>

    <UserPermissionDialog
      v-model="permissionDialogOpen"
      :user="permissionDialogUser"
      :roles="roles"
      @saved="refreshData"
    />

    <el-dialog v-model="importDialog" title="导入用户" width="560px">
      <el-form label-position="top">
        <el-form-item label="文件">
          <input type="file" accept=".csv,.xlsx" @change="onImportFileChange" />
        </el-form-item>
        <el-form-item label="冲突策略">
          <el-select v-model="importForm.conflictStrategy">
            <el-option label="跳过已有用户" value="skip" />
            <el-option label="更新已有用户" value="update" />
            <el-option label="覆盖已有用户" value="overwrite" />
            <el-option label="遇错停止" value="stop" />
          </el-select>
        </el-form-item>
        <el-form-item label="默认角色">
          <el-select v-model="importForm.defaultRoleId">
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="默认状态">
          <el-select v-model="importForm.defaultStatus">
            <el-option label="启用" value="active" />
            <el-option label="禁用" value="disabled" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importDialog = false">取消</el-button>
        <el-button type="primary" @click="importUsers">开始导入</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="exportDialog" title="导出用户" width="520px">
      <el-form label-position="top">
        <el-form-item label="格式">
          <el-select v-model="exportForm.format">
            <el-option label="CSV" value="csv" />
            <el-option label="XLSX" value="xlsx" />
          </el-select>
        </el-form-item>
        <el-form-item label="角色筛选">
          <el-select v-model="exportForm.roleId" clearable>
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="状态筛选">
          <el-select v-model="exportForm.status" clearable>
            <el-option label="启用" value="active" />
            <el-option label="禁用" value="disabled" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="exportDialog = false">取消</el-button>
        <el-button type="primary" @click="openExport">导出</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="shareDialog" title="创建文件共享" width="620px">
      <el-form label-position="top">
        <el-form-item label="文件">
          <el-select v-model="shareForm.fileId">
            <el-option v-for="file in files" :key="file.id" :label="file.originalName" :value="file.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="可见范围">
          <el-select v-model="shareForm.visibility">
            <el-option label="所有登录用户" value="all_users" />
            <el-option label="指定用户" value="specified_users" />
            <el-option label="指定角色" value="specified_roles" />
            <el-option label="指定设备" value="specified_devices" />
            <el-option label="指定 IP" value="specified_ips" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="shareForm.visibility === 'specified_users'" label="允许用户 ID">
          <el-input v-model="shareForm.allowedUserIdsText" type="textarea" :rows="3" placeholder="每行一个用户 ID 或用逗号分隔" />
        </el-form-item>
        <el-form-item v-if="shareForm.visibility === 'specified_ips'" label="允许 IP">
          <el-input v-model="shareForm.allowedIPsText" type="textarea" :rows="3" placeholder="每行一个 IP 或用逗号分隔" />
        </el-form-item>
        <el-form-item v-if="shareForm.visibility === 'specified_roles'" label="允许角色">
          <el-select v-model="shareForm.allowedRoleIds" multiple>
            <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="shareForm.visibility === 'specified_devices'" label="允许设备">
          <el-select v-model="shareForm.allowedDeviceIds" multiple>
            <el-option v-for="device in devices" :key="device.id" :label="`${device.deviceName} (${device.id.slice(0, 8)})`" :value="device.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="有效期">
          <el-input v-model="shareForm.expiresAt" placeholder="例如 2026-12-31T23:59:59，留空表示永久" />
        </el-form-item>
        <el-form-item label="最大下载次数">
          <el-input-number v-model="shareForm.maxDownloads" :min="0" :max="9999" />
        </el-form-item>
        <el-form-item label="文件密码">
          <el-switch v-model="shareForm.requireFilePassword" active-text="启用独立密码" />
        </el-form-item>
        <el-form-item v-if="shareForm.requireFilePassword" label="密码">
          <el-input v-model="shareForm.filePassword" show-password type="password" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="shareDialog = false">取消</el-button>
        <el-button type="primary" @click="createShare">创建共享</el-button>
      </template>
    </el-dialog>
  </div>
</template>
