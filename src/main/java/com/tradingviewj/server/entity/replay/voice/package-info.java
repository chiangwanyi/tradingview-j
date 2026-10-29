/**
 * 语音：录音段、语音状态与开段/清空负载。
 *
 * <p>音频本体是 wav 文件，这里只有元数据（哪一段、多长、哪个文件）。`StepContext` 放在这里是因为
 * 语音不靠广播驱动那一步，位置要从「最近一步」单独取。
 */
package com.tradingviewj.server.entity.replay.voice;
