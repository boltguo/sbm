export const historyMessages = {
  'zh-CN': {
    'history.title': '流量历史', 'history.help': '按天记录，按月汇总；重置套餐用量不会清除历史。',
    'history.granularity': '统计粒度', 'history.daily': '每日', 'history.monthly': '每月',
    'history.selectMonth': '选择月份', 'history.selectYear': '选择年份', 'history.day': '日期', 'history.month': '月份',
    'history.proxyTotal': '代理流量', 'history.providerTotal': '估算套餐用量', 'history.since': '开始记录：{date} · {timezone}',
    'history.importedHelp': '升级前已有 {amount} 的累计流量，无法拆分到每天；仅在整个汇总属于同一个月时计入该月。',
    'history.imported': '含升级前汇总', 'history.partial': '未完整记录', 'history.tableLabel': '流量记录 · {timezone}',
    'history.empty': '所选时间没有已保存的记录。', 'history.loading': '正在读取流量历史…', 'history.failed': '读取流量历史失败',
    'history.swipe': '左右滑动查看全部列', 'history.invalidPeriod': '请选择有效的月份或年份。',
    'history.footnote': '今天、本月和采样中断的记录可能不完整。中断期间的流量在恢复采样时入账，日归属可能有偏差；套餐用量按采样时的计费方式估算。历史每 30 秒保存一次，页面每分钟自动刷新；也可手动刷新查看最新已保存记录。',
  },
  en: {
    'history.title': 'Traffic history', 'history.help': 'Daily records and monthly totals survive plan usage resets.',
    'history.granularity': 'Summary interval', 'history.daily': 'Daily', 'history.monthly': 'Monthly',
    'history.selectMonth': 'Select month', 'history.selectYear': 'Select year', 'history.day': 'Date', 'history.month': 'Month',
    'history.proxyTotal': 'Proxy traffic', 'history.providerTotal': 'Estimated plan usage', 'history.since': 'Recording since {date} · {timezone}',
    'history.importedHelp': '{amount} of pre-upgrade usage cannot be split into days. It joins a monthly total only if the entire imported period falls in that month.',
    'history.imported': 'Includes imported usage', 'history.partial': 'Partial record', 'history.tableLabel': 'Traffic records · {timezone}',
    'history.empty': 'No saved records for the selected period.', 'history.loading': 'Reading traffic history…', 'history.failed': 'Could not read traffic history',
    'history.swipe': 'Swipe horizontally to see all columns', 'history.invalidPeriod': 'Select a valid month or year.',
    'history.footnote': 'Today, this month, and sampling gaps may be incomplete. Traffic accumulated during an outage is recorded when sampling resumes; its daily attribution may be inaccurate. Plan usage uses the billing mode at sampling time. History is saved every 30 seconds and refreshed here every minute. Refresh manually to see the latest saved records.',
  },
}
