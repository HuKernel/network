<script setup lang="ts">
import { reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { request } from '../lib/api'

type RoleItem = {
  id: string
  name: string
}

type UserTarget = {
  id: string
  username: string
}

const props = defineProps<{
  modelValue: boolean
  user: UserTarget | null
  roles: RoleItem[]
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: []
}>()

const form = reactive({
  roleId: '',
  status: 'active',
  canUpload: true,
  canDownload: true,
  canViewSharedList: true,
  canCreateShare: false,
  canManageOwnFiles: true,
  maxFileSizeMB: 0,
  dailyUploadLimitMB: 0,
  expiresAt: '',
  forceRelogin: true,
  forcePasswordChange: false,
  reason: '',
})

async function loadPermissions() {
  if (!props.user) return
  try {
    const data = await request(`/api/admin/users/${props.user.id}/permissions`)
    Object.assign(form, {
      roleId: data.roleId || '',
      status: data.status || 'active',
      canUpload: !!data.canUpload,
      canDownload: !!data.canDownload,
      canViewSharedList: !!data.canViewSharedList,
      canCreateShare: !!data.canCreateShare,
      canManageOwnFiles: !!data.canManageOwnFiles,
      maxFileSizeMB: data.maxFileSizeMB || 0,
      dailyUploadLimitMB: data.dailyUploadLimitMB || 0,
      expiresAt: data.expiresAt || '',
      forceRelogin: true,
      forcePasswordChange: !!data.mustChangePassword,
      reason: '',
    })
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

async function save() {
  if (!props.user) return
  try {
    await request(`/api/admin/users/${props.user.id}/permissions`, {
      method: 'PUT',
      body: JSON.stringify(form),
    })
    ElMessage.success('权限已更新')
    emit('saved')
    emit('update:modelValue', false)
  } catch (error: any) {
    ElMessage.error(error.message)
  }
}

watch(
  () => [props.modelValue, props.user?.id],
  ([open]) => {
    if (open) {
      void loadPermissions()
    }
  },
  { immediate: true },
)
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    width="640px"
    title="编辑用户权限"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form label-position="top">
      <el-form-item label="用户名">
        <el-input :model-value="user?.username || ''" disabled />
      </el-form-item>
      <el-form-item label="账号状态">
        <el-select v-model="form.status">
          <el-option label="启用" value="active" />
          <el-option label="禁用" value="disabled" />
          <el-option label="待启用" value="pending" />
        </el-select>
      </el-form-item>
      <el-form-item label="角色">
        <el-select v-model="form.roleId">
          <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="基础权限">
        <div class="switch-grid">
          <el-switch v-model="form.canUpload" active-text="允许上传" />
          <el-switch v-model="form.canDownload" active-text="允许下载" />
          <el-switch v-model="form.canViewSharedList" active-text="允许查看共享列表" />
          <el-switch v-model="form.canCreateShare" active-text="允许创建共享" />
          <el-switch v-model="form.canManageOwnFiles" active-text="允许管理自己上传的文件" />
        </div>
      </el-form-item>
      <el-form-item label="上传限制">
        <div class="number-grid">
          <el-input-number v-model="form.maxFileSizeMB" :min="0" :max="102400" />
          <span>单文件最大 MB，0 表示不限制</span>
        </div>
        <div class="number-grid">
          <el-input-number v-model="form.dailyUploadLimitMB" :min="0" :max="1048576" />
          <span>每日上传总量 MB，0 表示不限制</span>
        </div>
      </el-form-item>
      <el-form-item label="账号有效期">
        <el-input v-model="form.expiresAt" placeholder="例如 2026-12-31T23:59:59，留空表示永久" />
      </el-form-item>
      <el-form-item label="安全选项">
        <div class="switch-grid">
          <el-switch v-model="form.forceRelogin" active-text="保存后强制重新登录" />
          <el-switch v-model="form.forcePasswordChange" active-text="强制下次登录修改密码" />
        </div>
      </el-form-item>
      <el-form-item label="修改原因">
        <el-input v-model="form.reason" type="textarea" :rows="3" placeholder="可选，用于审计日志" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" @click="save">保存修改</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.switch-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
  width: 100%;
}

.number-grid {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
</style>
