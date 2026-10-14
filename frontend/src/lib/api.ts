// 统一 API 层：fetch 封装 + 带上传进度的 XHR
export async function request<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    credentials: 'include',
    headers: {
      ...(options.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
      ...(options.headers || {}),
    },
    ...options,
  })
  const contentType = response.headers.get('content-type') || ''
  const data = contentType.includes('application/json') ? await response.json() : await response.text()
  if (!response.ok) {
    throw new Error(typeof data === 'string' ? data : data.message || '请求失败')
  }
  return data
}

// 上传走 XHR：fetch 拿不到上传进度
export function upload<T = any>(path: string, form: FormData, onProgress?: (percent: number) => void): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', path)
    xhr.withCredentials = true
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      let data: any = xhr.responseText
      if ((xhr.getResponseHeader('content-type') || '').includes('application/json')) {
        try { data = JSON.parse(xhr.responseText || '{}') } catch { /* 保留原始文本 */ }
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new Error(data?.message || '请求失败'))
    }
    xhr.onerror = () => reject(new Error('网络错误，上传失败'))
    xhr.send(form)
  })
}
