import { ref, watch } from 'vue'

/**
 * 交易记录页的**界面偏好**：来源筛选、标签筛选。
 *
 * 为什么不放进 Pinia 的 replay store：这些不是「盘面状态」，而是「我正在怎么看这份
 * 报表」。换一张页面（日历 → 当日详情 → 统计）时它必须还在，刷新浏览器之后也该还在
 * —— 所以做成模块级单例 + 本机存档，navigate 多少次都是同一份。
 *
 * 标签筛选**不**持久化：它是一次性的查看动作（「今天我想看追单的那几笔」），
 * 留到下次反而会让人以为记录丢了。
 *
 * 这里只放「怎么看」，不放「看到了什么」—— 记录本身始终来自 store（服务端）。
 */

const SOURCE_KEY = 'tv-records-source'

function readSource(): string {
  try {
    return localStorage.getItem(SOURCE_KEY) ?? ''
  } catch {
    // 隐私模式：记不住就算了，不影响本次使用
    return ''
  }
}

/** 来源筛选键：'' 全部 / 'human' / 'bot' / `bot:<策略 id>` */
export const sourceFilter = ref(readSource())

watch(sourceFilter, (v) => {
  try {
    localStorage.setItem(SOURCE_KEY, v)
  } catch {
    // 同上
  }
})

/** 统计页的标签筛选（标签名小写，多选为「同时满足」） */
export const tagFilter = ref<string[]>([])
