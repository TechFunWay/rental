// usePhotoWall.ts — 「照片墙」的公共逻辑：取数、上传、删除，以及缩略图对象地址
// （照片必须带登录态读，<img src> 直接取不到，只能先取 blob 再转 objectURL）的
// 创建与回收。
//
// 抽出来的原因：抄表照片（MeterPhotoPicker）与房源照片（RoomPhotoSection）除了
// 「调哪个接口」之外完全一样——加载态、错误提示、张数上限、竞态保护、objectURL
// 回收都得一样，复制两份必然会漂移。两处各自只写一个适配器（load/upload/remove/
// fetchBlob），其余交给这里；视图统一由 PhotoWall.vue 渲染。
import { onBeforeUnmount, reactive, ref, type Ref } from 'vue'

import { toast } from '../utils/toast'

/** 照片墙里的最小字段约定（MeterPhoto / RoomPhoto 都满足）。 */
export interface PhotoWallItem {
  id: number
  file_name: string
}

/** 适配器返回口径：沿用后端 { code, message, data } 的三个关键字段。 */
export interface PhotoWallReply<T> {
  code?: number
  message?: string
  items?: T[]
  uploaded?: number
}

export interface PhotoWallOptions<T extends PhotoWallItem> {
  /** 文案用的照片名，如「水表」「房间」 */
  label: string
  /** 张数上限（与服务端一致），到了就不再显示拍照入口 */
  max: number
  load: () => Promise<PhotoWallReply<T>>
  upload: (files: File[]) => Promise<PhotoWallReply<T>>
  remove: (id: number) => Promise<PhotoWallReply<T>>
  fetchBlob: (id: number) => Promise<Blob>
  /** 张数变化后通知外层（如刷新列表上的照片数） */
  onChanged?: () => void
}

export function usePhotoWall<T extends PhotoWallItem>(opts: PhotoWallOptions<T>) {
  const photos = ref([]) as Ref<T[]>
  // 缩略图 id → blob URL
  const thumbs = reactive<Record<number, string>>({})
  const loading = ref(false)
  const uploading = ref(false)
  const error = ref('')
  // 竞态保护：数据源（房间/账期/表种）快速切换时，旧请求回来不许覆盖新结果
  let loadToken = 0

  function releaseThumbs() {
    for (const key of Object.keys(thumbs)) {
      URL.revokeObjectURL(thumbs[Number(key)])
      delete thumbs[Number(key)]
    }
  }

  async function load() {
    const token = ++loadToken
    loading.value = true
    error.value = ''
    try {
      const res = await opts.load()
      if (token !== loadToken) return
      photos.value = res.code === 0 ? (res.items ?? []) : []
      releaseThumbs()
      await Promise.all(photos.value.map(async (p) => {
        try {
          const blob = await opts.fetchBlob(p.id)
          if (token !== loadToken) return
          thumbs[p.id] = URL.createObjectURL(blob)
        } catch {
          // 单张缩略图失败不阻塞其余，也不弹错（用户可重新打开再看）
        }
      }))
    } catch {
      if (token === loadToken) error.value = '加载照片失败'
    } finally {
      if (token === loadToken) loading.value = false
    }
  }

  // pick 上传选中的照片：按剩余额度截断（服务端单次上限 6 张，剩余额度必然 ≤ 上限）
  async function pick(files: File[]) {
    const remain = opts.max - photos.value.length
    if (remain <= 0) {
      error.value = `最多 ${opts.max} 张，请先删除不用的`
      return
    }
    const picked = files.slice(0, remain)
    if (picked.length < files.length) toast(`最多 ${opts.max} 张，本次只上传前 ${picked.length} 张`, 'error')
    uploading.value = true
    error.value = ''
    try {
      const res = await opts.upload(picked)
      if (res.code === 0) {
        toast(`已上传 ${res.uploaded ?? picked.length} 张${opts.label}照片`)
        await load()
        opts.onChanged?.()
      } else {
        error.value = res.message || '上传失败'
      }
    } catch (err: any) {
      error.value = err.response?.data?.message || '上传失败，请重试'
    } finally {
      uploading.value = false
    }
  }

  async function remove(id: number) {
    error.value = ''
    try {
      const res = await opts.remove(id)
      if (res.code === 0) {
        toast('已删除照片')
        await load()
        opts.onChanged?.()
      } else {
        error.value = res.message || '删除失败'
      }
    } catch (err: any) {
      error.value = err.response?.data?.message || '删除失败，请重试'
    }
  }

  onBeforeUnmount(() => {
    loadToken++
    releaseThumbs()
  })

  return { photos, thumbs, loading, uploading, error, load, pick, remove }
}
