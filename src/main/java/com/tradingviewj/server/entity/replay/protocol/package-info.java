/**
 * 连接与上行：`Request`（全部上行消息）、欢迎/心跳/离开/错误/删除、成员与控制权。
 *
 * <p>这一包和业务无关，只描述「谁连着、谁在控盘、发来了什么」；业务负载一律回到它所属的那一包。
 */
package com.tradingviewj.server.entity.replay.protocol;
