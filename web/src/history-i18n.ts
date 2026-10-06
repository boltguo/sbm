export const historyMessages = {
  'zh-CN': {
    'history.title': '流量历史', 'history.help': 'vnStat 公网网卡流量按天记录、按自然月汇总；sing-box 仅作参考。套餐重置不会清除历史。',
    'history.granularity': '统计粒度', 'history.daily': '每日', 'history.monthly': '每月',
    'history.selectMonth': '选择月份', 'history.selectYear': '选择年份', 'history.day': '日期', 'history.month': '月份',
    'history.proxyTotal': '代理流量', 'history.providerTotal': '估算套餐用量', 'history.since': '开始记录：{date} · {timezone}',
    'history.importedHelp': '升级前已有 {amount} 的 sing-box 累计记录，保留作参考，无法拆分到每天；不会计入 vnStat 流量。',
    'history.imported': '含升级前汇总', 'history.partial': '未完整记录', 'history.tableLabel': '流量记录 · {timezone}',
    'history.empty': '所选时间没有已保存的记录。', 'history.loading': '正在读取流量历史…', 'history.failed': '读取流量历史失败',
    'history.swipe': '左右滑动查看全部列', 'history.invalidPeriod': '请选择有效的月份或年份。',
    'history.footnote': 'RX + TX 是整机公网网卡总量，不再乘二；单向套餐按 TX 计费。vnStat 每 30 秒读取并保存，页面每分钟刷新；恢复读取后会补读 vnStat 保留的记录。当天、本月、安装前及数据缺口标记为不完整。sing-box 参考仅包含代理业务，口径不同。',
  },
  en: {
    'history.title': 'Traffic history', 'history.help': 'vnStat public-interface traffic by day and calendar month, with sing-box as a reference. Plan resets keep history.',
    'history.granularity': 'Summary interval', 'history.daily': 'Daily', 'history.monthly': 'Monthly',
    'history.selectMonth': 'Select month', 'history.selectYear': 'Select year', 'history.day': 'Date', 'history.month': 'Month',
    'history.proxyTotal': 'Proxy traffic', 'history.providerTotal': 'Estimated plan usage', 'history.since': 'Recording since {date} · {timezone}',
    'history.importedHelp': '{amount} of legacy sing-box usage is retained as a reference and cannot be split into days. It is excluded from vnStat totals.',
    'history.imported': 'Includes imported usage', 'history.partial': 'Partial record', 'history.tableLabel': 'Traffic records · {timezone}',
    'history.empty': 'No saved records for the selected period.', 'history.loading': 'Reading traffic history…', 'history.failed': 'Could not read traffic history',
    'history.swipe': 'Swipe horizontally to see all columns', 'history.invalidPeriod': 'Select a valid month or year.',
    'history.footnote': 'RX + TX is whole-host public-interface traffic and is not doubled. One-way plans use TX. vnStat is read and saved every 30 seconds; this page refreshes each minute. Retained source records are recovered when reads resume. Current periods, pre-installation dates and coverage gaps are marked partial. sing-box counts proxy traffic only, so its reference uses a different scope.',
  },
}
