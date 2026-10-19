// 判断一个地址是不是「局域网/本机」地址。
//
// 用途（2026-10-13 房东反馈）：租客登记链接在飞牛网关域上会自动换成应用直连地址，
// 房东 NAS 是内网 IP（如 172.18.x.x、192.168.x.x）时，这条链接只有同一局域网里的
// 人打得开；外网租客点了必然超时。弹窗据此提示房东去填公网基址。

/** 从 URL 或 host:port 字符串里取出主机名部分。 */
export function hostnameOf(raw: string): string {
  let s = raw.trim()
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) s = 'http://' + s
  try {
    return new URL(s).hostname
  } catch {
    return ''
  }
}

/** IPv4 私有段：10/8、172.16/12、192.168/16、127/8、169.254/16。 */
export function isPrivateIPv4(host: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host)
  if (!m) return false
  const [a, b] = [Number(m[1]), Number(m[2])]
  if (a === 10 || a === 127) return true
  if (a === 172 && b >= 16 && b <= 31) return true
  if (a === 192 && b === 168) return true
  if (a === 169 && b === 254) return true
  return false
}

/** 主机名是不是局域网地址（私有 IPv4、localhost、.local 域名）。 */
export function isLanAddress(raw: string): boolean {
  const host = hostnameOf(raw)
  if (!host) return false
  if (host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.local')) return true
  // 被方括号包住的 IPv6 本机/链路本地
  const bare = host.replace(/^\[|\]$/g, '')
  if (bare === '::1' || bare.startsWith('fe80:')) return true
  return isPrivateIPv4(bare)
}

/** 「协议 + 主机 + 端口」的规范化形式（缺省端口补齐），用于判断两个地址是不是同一个入口。 */
export function originKey(raw: string): string {
  let s = raw.trim()
  if (!s) return ''
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) s = 'http://' + s
  try {
    const u = new URL(s)
    const scheme = u.protocol.replace(':', '')
    const port = u.port || (scheme === 'https' ? '443' : '80')
    return `${scheme}://${u.hostname}:${port}`
  } catch {
    return ''
  }
}

/** 租客登记链接的可用范围：决定登记链接弹窗给房东什么提示。 */
export type ApplyLinkScope =
  /** 基址被填成了房东自己访问的飞牛网关地址 —— 租客必撞 NAS 登录 */
  | 'gateway'
  /** 基址是内网地址 —— 只有同一局域网打得开 */
  | 'lan'
  /** 管理员配置了公网基址（内网穿透/端口映射/域名），可直接分享 */
  | 'custom'
  /** 没配置，按当前访问地址推导 */
  | 'derived'

/**
 * 判定登记链接的可用范围。
 *
 * @param base        服务端算出的链接基址（空串表示还没拿到）
 * @param configured  基址是否来自「租客登记链接基址」配置
 * @param viaGateway  这次请求是否走飞牛统一网关
 * @param currentUrl  房东当前页面的完整地址（判断基址是不是抄了网关入口）
 */
export function resolveApplyLinkScope(
  base: string,
  { configured, viaGateway, currentUrl }: { configured: boolean; viaGateway: boolean; currentUrl: string },
): ApplyLinkScope {
  if (!base) return 'derived'
  // 配了基址、又和房东当前所在的入口（协议+主机+端口）完全一致 → 把网关地址抄进去了。
  // 只比主机名不够：网关常是 <主机>:5643，而应用直连是 <同主机>:8910，主机名相同。
  if (configured && viaGateway && originKey(base) === originKey(currentUrl)) return 'gateway'
  if (isLanAddress(base)) return 'lan'
  return configured ? 'custom' : 'derived'
}
