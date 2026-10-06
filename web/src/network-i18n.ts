export const networkMessages = {
  'zh-CN': {
    'network.source': '来源：vnStat · {interface}', 'network.waiting': 'vnStat 尚无可用数据',
    'network.interrupted': 'vnStat 读取中断，保留最后结果', 'network.partial': '当前周期记录不完整；缺失流量未估算补齐。',
    'network.rx': '接收 RX', 'network.tx': '发送 TX', 'network.total': '公网网卡流量',
    'network.reference': 'sing-box 参考', 'network.missing': '未记录 vnStat',
    'network.interface': 'vnStat 公网网卡', 'network.autoInterface': '留空自动选择默认路由网卡',
    'network.guideDelay': 'vnStat 会先缓存再写入数据库，页面有更新延迟。面板保存历史副本；重置只改变套餐周期，不清空 vnStat 或历史。公网网卡包含 SSH、系统更新、WireGuard 和代理等整机通信；云厂商账单仍以其后台为准。',
  },
  en: {
    'network.source': 'Source: vnStat · {interface}', 'network.waiting': 'No vnStat data available yet',
    'network.interrupted': 'vnStat unavailable; showing the last saved result', 'network.partial': 'Incomplete period coverage; missing bytes are not estimated.',
    'network.rx': 'Received RX', 'network.tx': 'Sent TX', 'network.total': 'Public interface traffic',
    'network.reference': 'sing-box reference', 'network.missing': 'No vnStat record',
    'network.interface': 'vnStat public interface', 'network.autoInterface': 'Leave blank to use the default-route interface',
    'network.guideDelay': 'vnStat caches traffic before saving its database, so updates have a delay. SBM retains a history copy. Resets change billing periods without clearing vnStat or history. Public-interface totals include SSH, updates, WireGuard and proxy traffic; provider bills remain authoritative.',
  },
}
