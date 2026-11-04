package com.tradingviewj.server.service;

import com.tradingviewj.server.config.AppProperties;
import com.tradingviewj.server.dto.AudioClipView;
import com.tradingviewj.server.entity.audit.AuditEvent;
import com.tradingviewj.server.entity.audit.payload.VoiceAddedPayload;
import com.tradingviewj.server.entity.audit.payload.VoiceClearedPayload;
import com.tradingviewj.server.entity.audit.payload.VoiceState;
import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import com.tradingviewj.server.entity.voice.AudioClip;
import com.tradingviewj.server.enums.Actor;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.AudioClipRepository;
import com.tradingviewj.server.storage.SymbolSpec;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.List;
import java.util.UUID;

/**
 * 语音讲解：挂在 <strong>（标的, K 线时间）</strong> 上的录音。
 *
 * <h2>归属：K 线，不是账户</h2>
 *
 * <p>和图形一样，语音不参与余额 / 回撤 / 一致性 / 盈利日里的任何一项统计，所以
 * {@code audio_clip} 上没有 {@code account_id}。命令仍然带 {@code accountId} ——
 * 那不是归属，是「现在谁在看这块盘面」：要靠它拿到标的和 {@code lastBarTime}（录在哪一根）。
 *
 * <h2>额度只有一份</h2>
 *
 * <p>同一根 K 线最多 {@value #MAX_PER_BAR} 段，按（标的, K 线时间）数，和 Go 版同一个数。
 * 上限随 {@link VoiceState#max()} 一起发下去，前端只照它显示，不自己数。
 *
 * <h2>音频落盘</h2>
 *
 * <p>{@code <audioRoot>/<标的目录>/<日期>/<uuid>.wav}，库里只存相对路径 —— 换盘符 / 换用户名
 * 之后库还能用。文件名不与主键绑定：主键由 {@code BaseEntity} 生成，而实体构造时就要文件名，
 * 两个 UUID 各自唯一，没必要为了「长得一样」绕一圈拿 id。
 *
 * <p>写盘失败<strong>不打断录音</strong>：库里那一行照写，界面上看得见（只是取不到音频），
 * 下一次重录会另起一份。语音丢了不会破坏任何账目。
 */
@Slf4j
@Service
@RequiredArgsConstructor
public class VoiceService {

    /** 同一根 K 线上最多几段。需求写死 9 条，和 Go 版 {@code maxClipsPerBar} 一致。 */
    public static final int MAX_PER_BAR = 9;

    /** 单段录音上限（字节）。16kHz/16bit/单声道 = 32KB/s，2MB 约一分钟讲解。 */
    private static final int MAX_BYTES = 2 << 20;

    /** 每秒多少字节：前端没报时长时按它反推。 */
    private static final int BYTES_PER_SECOND = 32_000;

    private final AudioClipRepository clips;
    private final PropFirmAccountService accounts;
    private final TradingService trading;
    private final AuditLogService audit;
    private final AppProperties props;

    // ---------------- 读 ----------------

    /** 某一根 K 线上的语音状态（快照、{@code bar_advanced}、两条语音广播共用）。 */
    @Transactional(readOnly = true)
    public VoiceState state(String symbolId, String barTime) {
        List<AudioClipView> list = clips.findBySymbolIdAndBarTimeOrderBySeqAsc(symbolId, barTime)
                .stream().map(AudioClipView::of).toList();
        return new VoiceState(barTime, list, total(symbolId), MAX_PER_BAR);
    }

    /** 整个标的的语音清单（按 K 线时间排序）。盘面上「别处还有几条」靠它。 */
    @Transactional(readOnly = true)
    public List<AudioClip> all(String symbolId) {
        return clips.findBySymbolIdOrderByBarTimeAscSeqAsc(symbolId);
    }

    /** 给快照用的视图版清单。 */
    @Transactional(readOnly = true)
    public List<AudioClipView> allViews(String symbolId) {
        return all(symbolId).stream().map(AudioClipView::of).toList();
    }

    /** 一段语音的 wav 文件。给 HTTP 路由用，**按主键查**，路径不出自请求参数。 */
    @Transactional(readOnly = true)
    public Path file(String clipId) {
        AudioClip clip = clips.findById(clipId)
                .orElseThrow(() -> new BusinessException("这条语音已经不在了：" + clipId));
        Path path = pathOf(clip.getFileName());
        if (!Files.isRegularFile(path)) {
            throw new BusinessException("这条语音的文件找不到：" + clip.getFileName());
        }
        return path;
    }

    // ---------------- 写 ----------------

    /**
     * 录一段语音，挂到账户当前停的那根 K 线上。
     *
     * <p>校验顺序「先便宜后贵」：条数 → base64 → 字节数 → 文件头。文件头必须是 RIFF/WAVE ——
     * 这段字节之后会以 {@code audio/wav} 发给浏览器，收一个任意二进制进来等于开了个内容不明的文件服务。
     *
     * @param audioData  16kHz / 16bit / 单声道 WAV 的 base64（前端 {@code ClipRecorder} 编好）
     * @param durationMs 前端量到的时长；为空或 ≤0 时按字节数反推
     */
    @Transactional
    public TradingService.Event add(String accountId, String audioData, Integer durationMs) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        SymbolSpec spec = trading.spec(account.getSymbolId());
        String barTime = account.getLastBarTime();
        if (barTime == null || barTime.isBlank()) {
            throw new BusinessException("这根 K 线上还没有 K 线数据：先点【下一根】揭示一根");
        }
        long existing = clips.countBySymbolIdAndBarTime(spec.code(), barTime);
        if (existing >= MAX_PER_BAR) {
            throw new BusinessException("一根 K 线最多 " + MAX_PER_BAR + " 段录音，这根已经录满了；点【下一根】可以接着录");
        }

        byte[] wav = decode(audioData);
        int ms = checkWav(wav, durationMs == null ? 0 : durationMs);

        String day = barTime.length() >= 10 ? barTime.substring(0, 10) : "unknown";
        String relative = spec.folder() + "/" + day + "/" + UUID.randomUUID() + ".wav";
        AudioClip clip = clips.save(AudioClip.record(spec.code(), barTime, (int) existing,
                relative, ms, wav.length, Actor.HUMAN));
        write(relative, wav);

        VoiceAddedPayload payload = new VoiceAddedPayload(
                AudioClipView.of(clip), state(spec.code(), barTime), allViews(spec.code()));
        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        log.debug("账户 {} 在 {} 上录了第 {} 段语音（{}ms，{} 字节）",
                accountId, barTime, existing + 1, ms, wav.length);
        return new TradingService.Event(accountId, payload.action(), payload, event.getSeq());
    }

    /** 清空**当前这根 K 线**上的语音（音频文件一起删，别只在界面上清）。 */
    @Transactional
    public TradingService.Event clear(String accountId) {
        PropFirmAccount account = accounts.requireExisting(accountId);
        SymbolSpec spec = trading.spec(account.getSymbolId());
        String barTime = account.getLastBarTime();
        List<AudioClip> removed = clips.findBySymbolIdAndBarTimeOrderBySeqAsc(spec.code(), barTime);
        if (removed.isEmpty()) {
            throw new BusinessException("这根 K 线上还没有录音");
        }
        for (AudioClip clip : removed) {
            try {
                Files.deleteIfExists(pathOf(clip.getFileName()));
            } catch (IOException e) {
                // 文件删不掉不影响「清单里已经没有了」这个事实，下次清同一根时会再试
                log.warn("语音文件删不掉 {}: {}", clip.getFileName(), e.getMessage());
            }
        }
        clips.deleteAll(removed);

        VoiceClearedPayload payload = new VoiceClearedPayload(
                removed.stream().map(AudioClipView::of).toList(),
                state(spec.code(), barTime), allViews(spec.code()));
        AuditEvent event = audit.append(accountId, spec.code(), barTime, Actor.HUMAN, payload);
        return new TradingService.Event(accountId, payload.action(), payload, event.getSeq());
    }

    // ---------------- 内部 ----------------

    private int total(String symbolId) {
        return (int) clips.countBySymbolId(symbolId);
    }

    private Path pathOf(String relative) {
        return Path.of(props.getAudioRoot()).resolve(relative);
    }

    private void write(String relative, byte[] data) {
        Path path = pathOf(relative);
        try {
            Files.createDirectories(path.getParent());
            Files.write(path, data);
        } catch (IOException e) {
            log.warn("语音写盘失败 {}: {}", relative, e.getMessage());
        }
    }

    private static byte[] decode(String audioData) {
        if (audioData == null || audioData.isBlank()) {
            throw new BusinessException("录音是空的");
        }
        // 解码前先按长度挡一道：2MB 的 wav 编成 base64 约 2.8MB
        if (audioData.length() > MAX_BYTES / 3 * 4 + 16) {
            throw new BusinessException("单段录音最多 " + (MAX_BYTES >> 20) + " MB");
        }
        try {
            return Base64.getDecoder().decode(audioData);
        } catch (IllegalArgumentException e) {
            throw new BusinessException("录音数据不是合法的 base64");
        }
    }

    /** 校验 WAV 并返回时长（毫秒）。错误文案就是界面上的原话。 */
    private static int checkWav(byte[] data, int durationMs) {
        if (data.length == 0) {
            throw new BusinessException("录音是空的");
        }
        if (data.length > MAX_BYTES) {
            throw new BusinessException("单段录音最多 " + (MAX_BYTES >> 20) + " MB");
        }
        if (data.length < 44
                || !"RIFF".equals(new String(data, 0, 4))
                || !"WAVE".equals(new String(data, 8, 4))) {
            throw new BusinessException("录音不是 WAV（RIFF/WAVE）格式");
        }
        return durationMs > 0 ? durationMs : (data.length - 44) * 1000 / BYTES_PER_SECOND;
    }
}
