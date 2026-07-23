import { ref } from 'vue'

export type Locale = 'zh-CN' | 'en'
type Params = Record<string, string | number>

const messages: Record<Locale, Record<string, string>> = {
  'zh-CN': {
    'app.title': 'SBM', language: 'EN', close: '关闭', logout: '退出', overview: '概览', protocols: '协议', settings: '设置', copied: '已复制', 'password.show': '显示密码', 'password.hide': '隐藏密码',
    'login.title': '管理员登录', 'login.help': '凭据仅用于当前服务器。', 'login.username': '用户名', 'login.password': '密码', 'login.loading': '验证中…', 'login.submit': '进入控制台',
    'dashboard.title': '运行概览', 'dashboard.reset': '重置流量', 'dashboard.restart': '重启核心', 'dashboard.exceeded': '配额已耗尽', 'dashboard.exceededHelp': 'sing-box 已停止。重置流量或在设置中提高限额后可恢复。', 'dashboard.running': '运行中', 'dashboard.stopped': '已停止', 'dashboard.noReset': '不自动重置', 'dashboard.totalRemaining': '总额 {total} · 剩余 {remaining}', 'dashboard.unlimitedHelp': '未设置总流量限制', 'dashboard.unlimited': '不限量', 'dashboard.upload': '上传', 'dashboard.download': '下载', 'dashboard.subscription': '总订阅', 'dashboard.subscriptionHelp': '协议变更会立即反映到这个地址，无需重新添加订阅。', 'dashboard.copy': '复制', 'dashboard.secretHelp': '订阅地址包含访问 Token，请勿公开分享。', 'dashboard.qrAlt': '总订阅二维码', 'dashboard.loading': '正在读取核心状态…', 'dashboard.resetConfirm': '确定清零本周期的上传和下载流量？', 'dashboard.resetDone': '流量已重置', 'dashboard.restartDone': 'sing-box 已重启', 'dashboard.copyDone': '订阅地址已复制', 'dashboard.checkUpdate': '检查面板新版本', 'dashboard.updateFound': '发现新版本 {version}，SSH 运行 sudo sbm 后选择 6 更新', 'dashboard.updateCurrent': '当前已是最新版本', 'dashboard.updateFailed': '版本检查失败，请稍后重试',
    'protocol.title': '协议管理', 'protocol.help': '每次变更都先经过 sing-box check，失败时自动回滚。', 'protocol.add': '新增协议', 'protocol.disable': '停用', 'protocol.enable': '启用', 'protocol.off': '关闭', 'protocol.copyLink': '复制链接', 'protocol.showQr': '显示二维码', 'protocol.edit': '编辑协议', 'protocol.delete': '删除协议', 'protocol.addAnother': '接入另一个协议', 'protocol.addAnotherHelp': '使用独立端口，不影响现有入站', 'protocol.newTitle': '新增协议', 'protocol.type': '协议类型', 'protocol.duplicate': '同一服务器上重复创建相同协议通常没有明显意义，除非使用不同端口或不同配置。', 'protocol.name': '节点名称', 'protocol.port': '监听端口', 'protocol.portHelp': '{network} 端口；还需在云安全组中放行', 'protocol.cancel': '取消', 'protocol.generate': '生成并应用', 'protocol.editTitle': '编辑协议', 'protocol.privateKey': 'Reality 私钥', 'protocol.publicKey': 'Reality 公钥', 'protocol.password': '认证密码', 'protocol.obfs': '混淆', 'protocol.obfsPassword': '混淆密码', 'protocol.firewall': '修改端口后，SBM 会处理 VPS 内部防火墙；云厂商安全组仍需手动检查。', 'protocol.apply': '校验并应用', 'protocol.qrAlt': '节点二维码', 'protocol.qrHelp': '扫描后直接导入该节点', 'protocol.created': '协议已创建并应用', 'protocol.saved': '配置已校验并应用', 'protocol.disabled': '协议已停用', 'protocol.enabled': '协议已启用', 'protocol.deleteConfirm': '确定删除“{name}”？', 'protocol.deleted': '协议已删除', 'protocol.linkCopied': '节点链接已复制', 'protocol.defaultName': '新节点',
    'settings.title': '设置', 'settings.help': '配额按所有代理入站的总上传与下载累计。', 'settings.traffic': '流量与周期', 'settings.trafficHelp': '设为 0 表示无限流量。达到限额时核心自动停止。', 'settings.total': '总流量（GB）', 'settings.autoReset': '自动重置', 'settings.noReset': '不自动重置', 'settings.monthly': '每月重置', 'settings.day': '每月日期', 'settings.timezone': '时区', 'settings.save': '保存流量设置', 'settings.subscription': '总订阅 Token', 'settings.subscriptionHelp': '重新生成后，所有使用旧地址的客户端都无法更新。', 'settings.copy': '复制', 'settings.regenerate': '重新生成 Token', 'settings.password': '管理员密码', 'settings.passwordHelp': '为防止会话被盗用，修改前需验证当前密码；成功后所有旧会话立即失效。', 'settings.currentPassword': '当前密码', 'settings.newPassword': '新密码', 'settings.confirmPassword': '确认新密码', 'settings.changePassword': '修改密码', 'settings.loading': '正在读取设置…', 'settings.saved': '设置已保存', 'settings.tokenConfirm': '旧订阅地址会立即失效，确定重新生成？', 'settings.tokenDone': '订阅 Token 已重新生成', 'settings.passwordMismatch': '两次输入的新密码不一致', 'settings.passwordDone': '密码已修改，请重新登录',
    'api.failed': '请求失败 ({status})',
  },
  en: {
    'app.title': 'SBM', language: '中文', close: 'Close', logout: 'Sign out', overview: 'Overview', protocols: 'Protocols', settings: 'Settings', copied: 'Copied', 'password.show': 'Show password', 'password.hide': 'Hide password',
    'login.title': 'Administrator sign in', 'login.help': 'These credentials stay on this server.', 'login.username': 'Username', 'login.password': 'Password', 'login.loading': 'Verifying…', 'login.submit': 'Open console',
    'dashboard.title': 'Runtime overview', 'dashboard.reset': 'Reset traffic', 'dashboard.restart': 'Restart core', 'dashboard.exceeded': 'Quota exhausted', 'dashboard.exceededHelp': 'sing-box has stopped. Reset traffic or increase the quota in Settings to recover.', 'dashboard.running': 'Running', 'dashboard.stopped': 'Stopped', 'dashboard.noReset': 'No automatic reset', 'dashboard.totalRemaining': 'Total {total} · {remaining} remaining', 'dashboard.unlimitedHelp': 'No traffic limit configured', 'dashboard.unlimited': 'Unlimited', 'dashboard.upload': 'Upload', 'dashboard.download': 'Download', 'dashboard.subscription': 'Master subscription', 'dashboard.subscriptionHelp': 'Protocol changes appear at this URL immediately—no need to add it again.', 'dashboard.copy': 'Copy', 'dashboard.secretHelp': 'This URL contains an access token. Keep it private.', 'dashboard.qrAlt': 'Master subscription QR code', 'dashboard.loading': 'Reading core status…', 'dashboard.resetConfirm': 'Clear upload and download usage for the current period?', 'dashboard.resetDone': 'Traffic has been reset', 'dashboard.restartDone': 'sing-box restarted', 'dashboard.copyDone': 'Subscription URL copied', 'dashboard.checkUpdate': 'Check for a panel update', 'dashboard.updateFound': 'SBM {version} is available. Run sudo sbm over SSH and choose option 6.', 'dashboard.updateCurrent': 'SBM is up to date', 'dashboard.updateFailed': 'Could not check for updates. Try again later.',
    'protocol.title': 'Protocol management', 'protocol.help': 'Every change passes sing-box check first and rolls back automatically on failure.', 'protocol.add': 'Add protocol', 'protocol.disable': 'Disable', 'protocol.enable': 'Enable', 'protocol.off': 'Off', 'protocol.copyLink': 'Copy link', 'protocol.showQr': 'Show QR code', 'protocol.edit': 'Edit protocol', 'protocol.delete': 'Delete protocol', 'protocol.addAnother': 'Add another protocol', 'protocol.addAnotherHelp': 'Use a separate port without disturbing current inbounds', 'protocol.newTitle': 'Add protocol', 'protocol.type': 'Protocol type', 'protocol.duplicate': 'Creating the same protocol more than once on one server is rarely useful unless it uses a different port or configuration.', 'protocol.name': 'Node name', 'protocol.port': 'Listen port', 'protocol.portHelp': '{network} port; allow it in your cloud security group too', 'protocol.cancel': 'Cancel', 'protocol.generate': 'Generate and apply', 'protocol.editTitle': 'Edit protocol', 'protocol.privateKey': 'Reality private key', 'protocol.publicKey': 'Reality public key', 'protocol.password': 'Authentication password', 'protocol.obfs': 'Obfuscation', 'protocol.obfsPassword': 'Obfuscation password', 'protocol.firewall': 'SBM handles the firewall inside the VPS after a port change. You must still check the cloud security group.', 'protocol.apply': 'Validate and apply', 'protocol.qrAlt': 'Node QR code', 'protocol.qrHelp': 'Scan to import this node directly', 'protocol.created': 'Protocol created and applied', 'protocol.saved': 'Configuration validated and applied', 'protocol.disabled': 'Protocol disabled', 'protocol.enabled': 'Protocol enabled', 'protocol.deleteConfirm': 'Delete “{name}”?', 'protocol.deleted': 'Protocol deleted', 'protocol.linkCopied': 'Node link copied', 'protocol.defaultName': 'New node',
    'settings.title': 'Settings', 'settings.help': 'The quota combines upload and download across every proxy inbound.', 'settings.traffic': 'Traffic and period', 'settings.trafficHelp': 'Set 0 for unlimited traffic. The core stops automatically at the limit.', 'settings.total': 'Total traffic (GB)', 'settings.autoReset': 'Automatic reset', 'settings.noReset': 'No automatic reset', 'settings.monthly': 'Monthly', 'settings.day': 'Day of month', 'settings.timezone': 'Timezone', 'settings.save': 'Save traffic settings', 'settings.subscription': 'Master subscription token', 'settings.subscriptionHelp': 'Regenerating it prevents every client using the old URL from updating.', 'settings.copy': 'Copy', 'settings.regenerate': 'Regenerate token', 'settings.password': 'Administrator password', 'settings.passwordHelp': 'Re-enter the current password to prevent session abuse. A successful change invalidates every old session.', 'settings.currentPassword': 'Current password', 'settings.newPassword': 'New password', 'settings.confirmPassword': 'Confirm new password', 'settings.changePassword': 'Change password', 'settings.loading': 'Reading settings…', 'settings.saved': 'Settings saved', 'settings.tokenConfirm': 'The old subscription URL will stop working immediately. Regenerate it?', 'settings.tokenDone': 'Subscription token regenerated', 'settings.passwordMismatch': 'The new passwords do not match', 'settings.passwordDone': 'Password changed. Sign in again.',
    'api.failed': 'Request failed ({status})',
  },
}

const serverMessages: Record<Locale, Record<string, string>> = {
  'zh-CN': {
    server: '服务器', 'server.title': '服务器状态', 'server.help': '当前 Linux 主机资源快照，每 5 秒自动刷新。', 'server.cpu': 'CPU 使用率', 'server.memory': '内存', 'server.disk': '根分区磁盘', 'server.usedOf': '已用 {used} / {total}', 'server.load': '系统负载', 'server.loadHelp': '过去 1、5、15 分钟', 'server.uptime': '运行时长', 'server.days': '{days} 天 {hours} 小时', 'server.hours': '{hours} 小时 {minutes} 分钟', 'server.hostname': '主机名', 'server.system': '操作系统', 'server.kernel': '内核', 'server.arch': '架构', 'server.cores': '{count} 核', 'server.updated': '更新于 {time}', 'server.loading': '正在读取服务器状态…',
  },
  en: {
    server: 'Server', 'server.title': 'Server status', 'server.help': 'A live snapshot of this Linux host, refreshed every five seconds.', 'server.cpu': 'CPU usage', 'server.memory': 'Memory', 'server.disk': 'Root filesystem', 'server.usedOf': '{used} used of {total}', 'server.load': 'System load', 'server.loadHelp': 'Over the last 1, 5, and 15 minutes', 'server.uptime': 'Uptime', 'server.days': '{days}d {hours}h', 'server.hours': '{hours}h {minutes}m', 'server.hostname': 'Hostname', 'server.system': 'Operating system', 'server.kernel': 'Kernel', 'server.arch': 'Architecture', 'server.cores': '{count} cores', 'server.updated': 'Updated {time}', 'server.loading': 'Reading server status…',
  },
}

function initialLocale(): Locale {
  const saved = localStorage.getItem('sbm_locale')
  if (saved === 'zh-CN' || saved === 'en') return saved
  const preferred = navigator.languages?.[0] || navigator.language || 'en'
  return preferred.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

export const locale = ref<Locale>(initialLocale())

export function t(key: string, params: Params = {}): string {
	if (key === 'confirm') return locale.value === 'zh-CN' ? '确认' : 'Confirm'
	let value = serverMessages[locale.value][key] || messages[locale.value][key] || serverMessages['zh-CN'][key] || messages['zh-CN'][key] || key
  for (const [name, replacement] of Object.entries(params)) value = value.replaceAll(`{${name}}`, String(replacement))
  return value
}

export function setLocale(value: Locale) {
  locale.value = value
  localStorage.setItem('sbm_locale', value)
  document.documentElement.lang = value
  document.title = t('app.title')
}

export function toggleLocale() { setLocale(locale.value === 'zh-CN' ? 'en' : 'zh-CN') }
export function dateLocale() { return locale.value === 'zh-CN' ? 'zh-CN' : 'en-US' }

document.documentElement.lang = locale.value
document.title = t('app.title')
