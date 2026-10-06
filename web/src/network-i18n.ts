export const networkMessages = {
  'zh-CN': {
    'network.source': '来源：vnStat · {interface}', 'network.waiting': 'vnStat 尚无可用数据',
    'network.interrupted': 'vnStat 读取中断，显示最后结果', 'network.partial': '当前周期记录不完整',
    'network.rx': '接收 RX', 'network.tx': '发送 TX', 'network.total': '公网网卡流量',
    'network.reference': 'sing-box 参考', 'network.missing': '未记录 vnStat',
    'network.interface': 'vnStat 公网网卡', 'network.autoInterface': '留空自动选择默认路由网卡',
    'network.guideDelay': '统计所选公网网卡的整机通信，包含代理、WireGuard、SSH 和系统更新。采集间隔 30 秒，vnStat 每分钟保存。',
  },
  en: {
    'network.source': 'Source: vnStat · {interface}', 'network.waiting': 'No vnStat data available yet',
    'network.interrupted': 'vnStat unavailable; showing the last saved result', 'network.partial': 'Incomplete period coverage',
    'network.rx': 'Received RX', 'network.tx': 'Sent TX', 'network.total': 'Public interface traffic',
    'network.reference': 'sing-box reference', 'network.missing': 'No vnStat record',
    'network.interface': 'vnStat public interface', 'network.autoInterface': 'Leave blank to use the default-route interface',
    'network.guideDelay': 'Whole-host traffic on the selected public interface, including proxy, WireGuard, SSH and system updates. Collected every 30 seconds; vnStat saves each minute.',
  },
}
