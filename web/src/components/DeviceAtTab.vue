<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Add20Regular,
  Code24Regular,
  Delete20Regular,
  Eraser20Regular,
  History20Regular,
  Play20Regular,
  Save20Regular,
  Send20Regular,
  Star20Regular,
  Warning24Regular
} from '@vicons/fluent'
import { AT_TEMPLATES } from '../constants/atTemplates'
import { devicesService } from '../services/devices'

const props = defineProps<{
  deviceId: string
  backendMode?: string
  atPort?: string
  running?: boolean
}>()

type ATHistoryItem = {
  ts: number
  cmd: string
  ok: boolean
  response: string
}

type SavedATCommand = {
  id: string
  label: string
  value: string
  createdAt: number
  updatedAt: number
  useCount: number
  lastUsedAt?: number
}

const SAVED_COMMANDS_STORAGE_KEY = 'vohive.at.savedCommands.v1'

const atCmd = ref('')
const atTemplate = ref('')
const atTimeoutMs = ref(10000)
const atSending = ref(false)
const atHistory = ref<ATHistoryItem[]>([])
const savedCommands = ref<SavedATCommand[]>([])
const savedCommandSearch = ref('')
let savedCommandsLoaded = false

const atTemplates = AT_TEMPLATES
const hasATPort = computed(() => String(props.atPort || '').trim().length > 0)
const canUseATTerminal = computed(() => Boolean(props.running) && hasATPort.value)
const trimmedATCmd = computed(() => String(atCmd.value || '').trim())
const isCurrentCommandSaved = computed(() => {
  const cmd = trimmedATCmd.value
  return Boolean(cmd && savedCommands.value.some((item) => item.value === cmd))
})
const filteredSavedCommands = computed(() => {
  const keyword = savedCommandSearch.value.trim().toLowerCase()
  const items = [...savedCommands.value].sort((a, b) => {
    const lastUsedDelta = (b.lastUsedAt || 0) - (a.lastUsedAt || 0)
    if (lastUsedDelta !== 0) return lastUsedDelta
    const useCountDelta = b.useCount - a.useCount
    if (useCountDelta !== 0) return useCountDelta
    return b.updatedAt - a.updatedAt
  })
  if (!keyword) return items
  return items.filter((item) => {
    return item.label.toLowerCase().includes(keyword) || item.value.toLowerCase().includes(keyword)
  })
})
const unavailableTitle = computed(() => {
  if (!props.running) return '当前设备未运行'
  if (!hasATPort.value) return '当前设备没有可用 AT 口'
  return 'AT 终端暂不可用'
})
const unavailableDescription = computed(() => {
  if (!props.running) {
    return '设备当前未启动，AT 终端暂时不可用。待设备运行后，如果存在可用的 AT 口，即可在这里直接发送 AT 指令。'
  }
  if (!hasATPort.value && props.backendMode === 'qmi') {
    return '设备当前处于纯 QMI 模式，但没有解析到可用的 AT 口，因此无法提供 AT 串口终端。'
  }
  if (!hasATPort.value) {
    return '设备当前没有可用的 AT 口，因此无法提供 AT 串口终端。'
  }
  return '当前设备暂时无法提供 AT 串口终端，请稍后重试。'
})

loadSavedCommands()

watch(
  () => atTemplate.value,
  (v) => {
    const cmd = String(v || '').trim()
    if (cmd) atCmd.value = cmd
  }
)

watch(
  savedCommands,
  () => {
    if (!savedCommandsLoaded) return
    persistSavedCommands()
  },
  { deep: true }
)

function loadSavedCommands() {
  savedCommandsLoaded = true
  if (typeof window === 'undefined') return
  try {
    const raw = window.localStorage.getItem(SAVED_COMMANDS_STORAGE_KEY)
    if (!raw) return
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return
    savedCommands.value = parsed
      .map((item): SavedATCommand | null => {
        if (!item || typeof item !== 'object') return null
        const payload = item as Partial<SavedATCommand>
        const value = String(payload.value || '').trim()
        if (!value) return null
        const now = Date.now()
        return {
          id: String(payload.id || `at-${now}-${Math.random().toString(16).slice(2)}`),
          label: String(payload.label || value).trim() || value,
          value,
          createdAt: Number(payload.createdAt || now),
          updatedAt: Number(payload.updatedAt || now),
          useCount: Number(payload.useCount || 0),
          lastUsedAt: Number(payload.lastUsedAt || 0) || undefined
        }
      })
      .filter((item): item is SavedATCommand => Boolean(item))
  } catch {
    savedCommands.value = []
  }
}

function persistSavedCommands() {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(SAVED_COMMANDS_STORAGE_KEY, JSON.stringify(savedCommands.value))
}

function defaultCommandLabel(cmd: string) {
  return cmd.length > 42 ? `${cmd.slice(0, 42)}...` : cmd
}

function fillCommand(cmd: string) {
  atCmd.value = cmd
}

function markSavedCommandUsed(cmd: string) {
  const item = savedCommands.value.find((it) => it.value === cmd)
  if (!item) return
  item.useCount += 1
  item.lastUsedAt = Date.now()
  item.updatedAt = item.updatedAt || item.lastUsedAt
}

async function saveCurrentCommand() {
  const cmd = trimmedATCmd.value
  if (!cmd) {
    ElMessage.warning('请输入要保存的 AT 指令')
    return
  }

  const existing = savedCommands.value.find((item) => item.value === cmd)
  const { value } = await ElMessageBox.prompt('给这条 AT 指令取一个名称', existing ? '更新常用指令' : '保存常用指令', {
    confirmButtonText: existing ? '更新' : '保存',
    cancelButtonText: '取消',
    inputValue: existing?.label || defaultCommandLabel(cmd),
    inputValidator: (input) => Boolean(String(input || '').trim()),
    inputErrorMessage: '名称不能为空'
  }).catch(() => ({ value: '' }))

  const label = String(value || '').trim()
  if (!label) return

  const now = Date.now()
  if (existing) {
    existing.label = label
    existing.updatedAt = now
    ElMessage.success('常用指令已更新')
    return
  }

  savedCommands.value.unshift({
    id: `at-${now}-${Math.random().toString(16).slice(2)}`,
    label,
    value: cmd,
    createdAt: now,
    updatedAt: now,
    useCount: 0
  })
  ElMessage.success('常用指令已保存')
}

function removeSavedCommand(id: string) {
  savedCommands.value = savedCommands.value.filter((item) => item.id !== id)
  ElMessage.success('已删除常用指令')
}

async function sendSavedCommand(item: SavedATCommand) {
  atCmd.value = item.value
  await sendAT()
}

async function sendAT() {
  const cmd = trimmedATCmd.value
  if (!cmd) return
  atSending.value = true
  try {
    const result = await devicesService.sendAT(props.deviceId, {
      cmd: cmd,
      timeout_ms: atTimeoutMs.value || 10000
    })
    if (!result.ok) throw new Error(result.error.message || '请求异常')
    atHistory.value.push({
      ts: Date.now(),
      cmd,
      ok: result.data.ok,
      response: result.data.response
    })
    markSavedCommandUsed(cmd)
  } catch (e: unknown) {
    atHistory.value.push({
      ts: Date.now(),
      cmd,
      ok: false,
      response: e instanceof Error ? e.message : '请求异常'
    })
  } finally {
    if (atHistory.value.length > 80) {
      atHistory.value = atHistory.value.slice(-80)
    }
    atSending.value = false
  }
}

function clearATHistory() {
  atHistory.value = []
}
</script>

<template>
  <div>
    <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
      <div class="flex items-center gap-3 min-w-0">
        <div class="w-10 h-10 rounded-xl bg-gray-100 dark:bg-gray-800 flex shrink-0 items-center justify-center text-gray-700 dark:text-gray-300">
        <el-icon size="22"><Code24Regular /></el-icon>
      </div>
        <div class="min-w-0">
          <div class="text-lg font-bold text-gray-900 dark:text-white">AT 终端</div>
        <div class="text-sm text-gray-500 dark:text-gray-400 mt-0.5">发送 AT 指令并查看回显（多行响应会完整返回）</div>
      </div>
    </div>
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="inline-flex items-center gap-1 rounded-full border border-gray-200 bg-white px-3 py-1.5 font-mono text-gray-600 dark:border-white/10 dark:bg-white/5 dark:text-gray-300">
          {{ atPort ? `AT: ${atPort}` : 'AT: --' }}
        </span>
        <span class="inline-flex items-center gap-1 rounded-full px-3 py-1.5 font-medium" :class="canUseATTerminal ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300' : 'bg-orange-50 text-orange-700 dark:bg-orange-500/10 dark:text-orange-300'">
          {{ canUseATTerminal ? '可用' : '不可用' }}
        </span>
      </div>
    </div>

    <template v-if="!canUseATTerminal">
      <div class="mt-4 p-8 flex flex-col items-center justify-center bg-orange-50 dark:bg-orange-900/20 border border-orange-100 dark:border-orange-900/50 rounded-xl">
        <el-icon size="48" class="text-orange-400 mb-4"><Warning24Regular /></el-icon>
        <div class="text-lg font-bold text-orange-700 dark:text-orange-400">{{ unavailableTitle }}</div>
        <div class="text-sm text-orange-600 dark:text-orange-300 mt-2 text-center max-w-md">
          {{ unavailableDescription }}
        </div>
      </div>
    </template>
    
    <template v-else>
      <div class="mt-4 grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_360px] gap-4">
        <section class="ui-panel-muted h-[620px] overflow-hidden flex flex-col">
          <div class="flex flex-col gap-3 border-b border-gray-100/80 p-4 dark:border-white/10 sm:flex-row sm:items-center sm:justify-between">
            <div class="flex items-center gap-2 text-sm font-bold text-gray-800 dark:text-gray-100">
              <el-icon><History20Regular /></el-icon>
              会话记录
            </div>
            <el-tooltip content="清空记录" placement="top">
              <el-button :disabled="atHistory.length === 0 && !atSending" @click="clearATHistory" class="ui-button-plain">
                <el-icon><Eraser20Regular /></el-icon>
                清空
              </el-button>
            </el-tooltip>
          </div>

          <div class="relative flex-1 overflow-auto p-4">
            <div v-if="atHistory.length === 0 && !atSending" class="absolute inset-0 flex items-center justify-center text-sm text-gray-400">
              暂无 AT 会话记录
            </div>
            <div class="flex flex-col gap-4">
              <div v-for="(h, i) in atHistory" :key="h.ts + h.cmd + i" class="flex flex-col gap-2 w-full">
                <div class="flex w-full justify-end">
                  <div class="max-w-[88%] bg-blue-600 text-white rounded-2xl rounded-tr-sm px-4 py-2.5 shadow-sm">
                    <div class="text-sm font-mono break-words">{{ h.cmd }}</div>
                    <div class="text-[10px] text-blue-100 mt-1 text-right">{{ new Date(h.ts).toLocaleTimeString() }}</div>
                  </div>
                </div>

                <div class="flex w-full justify-start">
                  <div class="max-w-[88%] rounded-2xl rounded-tl-sm px-4 py-2.5 shadow-sm" :class="!h.ok ? 'bg-red-50 dark:bg-red-900/30 text-red-700 dark:text-red-300 border border-red-100 dark:border-red-900/50' : 'bg-white dark:bg-gray-800 text-gray-800 dark:text-gray-200 border border-gray-100 dark:border-white/5'">
                    <div class="text-sm whitespace-pre-wrap break-words font-mono leading-relaxed">{{ h.response }}</div>
                    <div class="text-[10px] mt-1 text-gray-400 flex items-center gap-2">
                      <span>{{ new Date(h.ts).toLocaleTimeString() }}</span>
                    </div>
                  </div>
                </div>
              </div>

              <div v-if="atSending" class="flex w-full justify-start mt-1">
                <div class="max-w-[88%] bg-white dark:bg-gray-800 rounded-2xl rounded-tl-sm px-4 py-3 shadow-sm border border-gray-100 dark:border-white/5 flex items-center gap-2">
                  <div class="flex space-x-1">
                    <div class="w-1.5 h-1.5 bg-blue-400 rounded-full animate-bounce [animation-delay:-0.3s]"></div>
                    <div class="w-1.5 h-1.5 bg-blue-400 rounded-full animate-bounce [animation-delay:-0.15s]"></div>
                    <div class="w-1.5 h-1.5 bg-blue-400 rounded-full animate-bounce"></div>
                  </div>
                  <span class="text-xs text-gray-400 ml-1">等待模组响应...</span>
                </div>
              </div>
            </div>
          </div>

          <div class="border-t border-gray-100/80 bg-white/60 p-4 dark:border-white/10 dark:bg-white/[0.03]">
            <div class="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_120px_auto]">
              <div class="space-y-1">
                <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">命令</div>
                <el-input
                  v-model="atCmd"
                  placeholder='例如 AT+CSQ'
                  @keyup.enter="sendAT"
                  :disabled="atSending"
                />
              </div>

              <div class="space-y-1">
                <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">超时(ms)</div>
                <el-input v-model.number="atTimeoutMs" type="number" inputmode="numeric" placeholder="10000" :disabled="atSending" />
              </div>

              <div class="flex items-end justify-end gap-2">
                <el-tooltip :content="isCurrentCommandSaved ? '更新常用指令' : '保存常用指令'" placement="top">
                  <el-button :disabled="!trimmedATCmd || atSending" @click="saveCurrentCommand" class="ui-button-plain">
                    <el-icon><Save20Regular /></el-icon>
                  </el-button>
                </el-tooltip>
                <el-button type="primary" :loading="atSending" :disabled="!trimmedATCmd" @click="sendAT" class="!border-0">
                  <el-icon><Send20Regular /></el-icon>
                  发送
                </el-button>
              </div>
            </div>
          </div>
        </section>

        <aside class="flex flex-col gap-4">
          <section class="ui-panel overflow-hidden">
            <div class="flex items-center justify-between border-b border-gray-100/80 p-4 dark:border-white/10">
              <div class="flex items-center gap-2 text-sm font-bold text-gray-800 dark:text-gray-100">
                <el-icon><Star20Regular /></el-icon>
                常用指令
              </div>
              <el-tooltip content="保存当前命令" placement="top">
                <el-button :disabled="!trimmedATCmd" @click="saveCurrentCommand" class="ui-button-plain">
                  <el-icon><Add20Regular /></el-icon>
                </el-button>
              </el-tooltip>
            </div>
            <div class="p-4">
              <el-input v-model="savedCommandSearch" clearable placeholder="搜索常用指令" />
              <div v-if="filteredSavedCommands.length === 0" class="mt-4 rounded-lg border border-dashed border-gray-200 p-5 text-center text-sm text-gray-400 dark:border-white/10">
                暂无常用指令
              </div>
              <div v-else class="mt-4 flex max-h-[310px] flex-col gap-2 overflow-auto pr-1">
                <div
                  v-for="item in filteredSavedCommands"
                  :key="item.id"
                  class="group rounded-lg border border-gray-100 bg-white p-3 transition hover:border-blue-200 hover:bg-blue-50/50 dark:border-white/10 dark:bg-white/[0.04] dark:hover:border-blue-400/30 dark:hover:bg-blue-400/10"
                >
                  <button class="w-full text-left" type="button" @click="fillCommand(item.value)">
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <div class="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ item.label }}</div>
                        <div class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ item.value }}</div>
                      </div>
                      <div class="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-[10px] text-gray-500 dark:bg-white/10 dark:text-gray-400">
                        {{ item.useCount }}
                      </div>
                    </div>
                  </button>
                  <div class="mt-3 flex items-center justify-end gap-2">
                    <el-tooltip content="发送" placement="top">
                      <el-button :loading="atSending && atCmd === item.value" :disabled="atSending" @click="sendSavedCommand(item)" class="ui-button-plain">
                        <el-icon><Play20Regular /></el-icon>
                      </el-button>
                    </el-tooltip>
                    <el-tooltip content="删除" placement="top">
                      <el-button :disabled="atSending" @click="removeSavedCommand(item.id)" class="ui-button-plain hover:!text-red-600">
                        <el-icon><Delete20Regular /></el-icon>
                      </el-button>
                    </el-tooltip>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <section class="ui-panel overflow-hidden">
            <div class="flex items-center gap-2 border-b border-gray-100/80 p-4 text-sm font-bold text-gray-800 dark:border-white/10 dark:text-gray-100">
              <el-icon><Code24Regular /></el-icon>
              内置模板
            </div>
            <div class="p-4">
              <el-select v-model="atTemplate" filterable clearable placeholder="选择模板指令">
                <el-option-group v-for="g in atTemplates" :key="g.label" :label="g.label">
                  <el-option v-for="it in g.items" :key="it.value" :label="it.label" :value="it.value" />
                </el-option-group>
              </el-select>
              <div class="mt-3 flex flex-wrap gap-2">
                <button
                  v-for="item in atTemplates[0]?.items.slice(0, 6)"
                  :key="item.value"
                  type="button"
                  class="rounded-full border border-gray-200 bg-white px-3 py-1.5 font-mono text-xs text-gray-600 transition hover:border-blue-200 hover:text-blue-700 dark:border-white/10 dark:bg-white/[0.04] dark:text-gray-300 dark:hover:border-blue-400/40 dark:hover:text-blue-200"
                  @click="fillCommand(item.value)"
                >
                  {{ item.value }}
                </button>
              </div>
            </div>
          </section>
        </aside>
        </div>
    </template>
  </div>
</template>
