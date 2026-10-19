// 服务端业务错误的取文案助手。
//
// 背景（2026-10-13 粉丝反馈）：后端的业务失败统一走 response.Error*，HTTP 状态是
// 4xx、正文里带着明确的 {code,message}；axios 遇到非 2xx 会抛异常，各页面的
// catch 分支若只写「网络异常，请稍后再试」，就会把「AI 识别未配置」「手机号已提交过」
// 这类**说得很清楚的原因**吞掉，租客看不懂也改不了。

interface ServerErrorBody {
  code?: number
  message?: string
}

function errorBody(e: unknown): ServerErrorBody | undefined {
  const body = (e as { response?: { data?: unknown } })?.response?.data
  return body && typeof body === 'object' ? (body as ServerErrorBody) : undefined
}

/** 取服务端返回的业务码；没有响应（真·网络故障）时返回 undefined。 */
export function serverErrorCode(e: unknown): number | undefined {
  const code = errorBody(e)?.code
  return typeof code === 'number' ? code : undefined
}

/** 优先用服务端 message，没有响应或为空时用 fallback。 */
export function serverErrorMessage(e: unknown, fallback: string): string {
  const msg = errorBody(e)?.message
  return typeof msg === 'string' && msg.trim() ? msg : fallback
}
