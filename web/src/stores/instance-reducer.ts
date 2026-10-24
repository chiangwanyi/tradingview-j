/**
 * 一条广播对实例状态的**唯一**作用点。
 *
 * 为什么单独抽出来：实时界面和回放必须逐像素一致，而要做到这一点，最省事也最可靠的
 * 办法不是"把回放写得像实时"，而是**两边跑同一个函数**：
 *
 *   实时：WebSocket 广播 -> applyInstanceEvent(store.instance, ...)
 *   回放：录像里的 payload -> applyInstanceEvent(播放器状态, ...)
 *
 * 任何"回放时另外写一套更新逻辑"的做法，都会在某次改动后悄悄分叉 ——
 * 那时回放看起来"差不多"，但数字差一点，而且没人知道是哪一边错。
 *
 * 约定：只改盘面状态（K 线、标注、挂单、成交、持仓、盈亏、成员、操作权），
 * 不碰界面状态（选中项、日志、toast）—— 那些是调用方的事。
 */

import { ACTION } from '@/types/protocol'
import type {
  AnnotationClearedPayload,
  AnnotationDeletedPayload,
  AnnotationPayload,
  BarAdvancedPayload,
  BotPayload,
  ControlChangedPayload,
  FlatAllPayload,
  InstanceDetail,
  MembersChangedPayload,
  OrderFilledPayload,
  OrderMovedPayload,
  OrdersPayload,
} from '@/types/protocol'

export function applyInstanceEvent(inst: InstanceDetail, action: string, payload: unknown): void {
  switch (action) {
    case ACTION.barAdvanced: {
      const p = payload as BarAdvancedPayload
      inst.bars = [...inst.bars, p.bar]
      inst.cursor = p.cursor
      inst.hiddenBars = p.hiddenBars
      inst.hasMore = p.hasMore
      inst.orders = p.orders
      inst.trades = p.trades
      inst.closedLots = p.closedLots
      inst.position = p.position
      inst.realizedPnl = p.realizedPnl
      inst.unrealizedPnl = p.unrealizedPnl
      // 换了一根 K 线：语音的「本根 x/9」和清单整体换成新那根的。
      // 这一条**和有没有在录制无关**（v0.16.0），所以不走录制状态那条线。
      if (p.voice) inst.voice = p.voice
      break
    }
    case ACTION.annotationAdded: {
      const p = payload as AnnotationPayload
      inst.annotations = [...inst.annotations, p.annotation]
      break
    }
    case ACTION.annotationMoved:
    case ACTION.annotationStyled: {
      // 移动和换样式都是「同一个 id 换成新的一份」，走同一段替换：
      // 两条广播都带完整的 annotation（不是补丁），所以不需要区分字段。
      const p = payload as AnnotationPayload
      inst.annotations = inst.annotations.map((a) => (a.id === p.annotation.id ? p.annotation : a))
      break
    }
    case ACTION.annotationDeleted: {
      const p = payload as AnnotationDeletedPayload
      inst.annotations = inst.annotations.filter((a) => a.id !== p.annotationId)
      break
    }
    case ACTION.annotationCleared: {
      // 一次清空是**一个原子状态**：不按 payload 里的编号逐个过滤 ——
      // 服务端说的是「这个实例现在一个绘图都没有」，照它办就不会有漏网的那一个。
      const p = payload as AnnotationClearedPayload
      void p
      inst.annotations = []
      break
    }
    case ACTION.orderPlaced:
    case ACTION.orderCancelled: {
      const p = payload as OrdersPayload
      inst.orders = p.orders
      break
    }
    case ACTION.orderMoved: {
      const p = payload as OrderMovedPayload
      inst.orders = p.orders
      break
    }
    case ACTION.orderFilled: {
      // 市价单是同步成交的：订单、成交流水、持仓和盈亏一次到位
      const p = payload as OrderFilledPayload
      inst.orders = p.orders
      inst.trades = p.trades
      inst.closedLots = p.closedLots
      inst.position = p.position
      inst.realizedPnl = p.realizedPnl
      inst.unrealizedPnl = p.unrealizedPnl
      break
    }
    case ACTION.flatAll: {
      // 【市价退仓并取消所有挂单】：一次广播换掉整块交易状态。
      // 可能只撤了单（本来空仓），也可能既平了仓又撤了单 —— 两种情况字段形状一样，
      // 前端不做分支，直接照抄服务端给的这一份。
      const p = payload as FlatAllPayload
      inst.orders = p.orders
      inst.trades = p.trades
      inst.closedLots = p.closedLots
      inst.position = p.position
      inst.realizedPnl = p.realizedPnl
      inst.unrealizedPnl = p.unrealizedPnl
      break
    }
    case ACTION.membersChanged: {
      const p = payload as MembersChangedPayload
      inst.members = p.members
      break
    }
    case ACTION.controlChanged: {
      const p = payload as ControlChangedPayload
      inst.operator = p.operator
      break
    }
    case ACTION.botStarted: {
      // 带上策略 id：界面上要能说清「跑的是 Caveman 还是随机演示」。
      // 停止时对应地清空，否则下次启动随机演示还会顶着上一次的策略名。
      const p = payload as BotPayload | undefined
      inst.bot = true
      inst.botStrategy = p?.strategy ?? ''
      inst.botQuantity = p?.quantity ?? 0
      break
    }
    case ACTION.botStopped:
      inst.bot = false
      inst.botStrategy = ''
      inst.botQuantity = 0
      break
    default:
      break
  }
}

/** 录像里的 payload 是 JSON 原样存下来的，这里只做一次类型断言收口。 */
export type RecordedPayload = unknown
