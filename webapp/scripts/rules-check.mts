/**
 * 校验口径的自检（不引测试框架）：`npm run check`。
 *
 * 钉住的是「阶段与字段是否自洽」这几条，和后端 PropFirmTemplate.validate 一一对应。
 * 这几条错了不会在界面上显形，只会让账户永远升不了级，所以值得一个脚本盯着。
 */
import assert from 'node:assert/strict'

import { validate } from '../src/data/rules.ts'
import type { Rules, Template } from '../src/data/rules.ts'

const base = (overrides: Partial<Rules> = {}): Rules => ({
  accountSize: 50000,
  profitTarget: 3000,
  maxDrawdown: 2000,
  drawdownType: 'TRAILING_EOD',
  trailingLockLevel: 52100,
  consistencyRatio: 0.4,
  minProfitDays: null,
  profitDayThreshold: null,
  positionTiers: [{ fromProfit: 0, maxContracts: 40 }],
  ...overrides,
})

const tpl = (stage: Template['stage'], rules: Rules): Template => ({
  id: 't',
  name: 'LucidFlex 50K',
  description: '',
  stage,
  nextTemplateId: null,
  rules,
  createdAt: 0,
})

// 自洽的 EVAL 能过
assert.deepEqual(validate(tpl('EVAL', base())), [])

// EVAL 没有盈利目标 = 没有考试，不成立
assert.deepEqual(validate(tpl('EVAL', base({ profitTarget: null }))), ['EVAL 阶段必须有盈利目标'])

// FUNDED 带盈利目标 = 其实是第二场考试，不成立
assert.deepEqual(validate(tpl('FUNDED', base())), ['FUNDED 阶段不能有盈利目标（那是 EVAL 的规则）'])

// 自洽的 FUNDED 能过（无目标、无一致性、有盈利日）
assert.deepEqual(
  validate(tpl('FUNDED', base({ profitTarget: null, consistencyRatio: null, minProfitDays: 5 }))),
  [],
)

// 名字和规模是硬门槛
assert.deepEqual(
  validate({ ...tpl('EVAL', base()), name: '  ' }),
  ['模板必须有名字'],
)
assert.deepEqual(validate(tpl('EVAL', base({ accountSize: null }))), ['账户规模必须是正数'])

console.log('rules-check: 6 项通过')
