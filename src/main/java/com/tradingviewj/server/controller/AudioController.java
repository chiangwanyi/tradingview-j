package com.tradingviewj.server.controller;

import com.tradingviewj.server.service.VoiceService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.core.io.FileSystemResource;
import org.springframework.core.io.Resource;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * 语音讲解的音频文件：{@code GET /api/audio/<id>}。
 *
 * <p><strong>只按主键取</strong>，不接路径参数：文件名由后端拼（见 {@code VoiceService}），
 * 请求里出现的任何一段都不能变成磁盘路径 —— 这个接口把文件内容发给浏览器，
 * 一丁点路径拼接就等于开了一个「读任意本地文件」的口子。
 *
 * <p>返回 {@link Resource} 而不是 {@code byte[]} / {@code ResponseEntity}：
 * 全局响应包裹（{@code GlobalResponseAdvice}）只对 JSON 接口生效，二进制必须绕开它，
 * 而 {@code Resource} 正好在它的排除名单里。音频写完就不再变，可以放心让浏览器缓存。
 */
@Tag(name = "语音", description = "语音讲解的音频：按主键取一段 WAV")
@RestController
@RequestMapping("/api/audio")
@RequiredArgsConstructor
public class AudioController {

    private final VoiceService voice;

    @Operation(summary = "一段语音的 WAV", description = """
            id 是 audio_clip 的主键（随快照 / voice_added 广播下发）。
            文件不存在或已被清空时返回业务错误，不是空音频。
            """)
    @GetMapping(value = "/{id}", produces = "audio/wav")
    public Resource clip(@PathVariable String id) {
        return new FileSystemResource(voice.file(id));
    }
}
