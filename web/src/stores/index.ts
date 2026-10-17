import { createPinia } from 'pinia'

/**
 * 全局唯一的 Pinia 实例。在 main.ts 里 app.use(pinia)。
 * store 定义放同目录，一个文件一个 store，文件名即 store id。
 */
export const pinia = createPinia()
