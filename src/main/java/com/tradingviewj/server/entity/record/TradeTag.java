package com.tradingviewj.server.entity.record;

import com.tradingviewj.server.entity.BaseEntity;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Table;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

import java.util.regex.Pattern;

/**
 * 一个交易标签（{@code trade_tag}）：<strong>独立的东西，不属于任何一条交易记录</strong>。
 *
 * <h2>它和 {@code trade_record_tag} 的分工</h2>
 *
 * <p>「追单」「计划内」这种标签是用户自己攒出来的一本标签本子。它先于任何交易存在，
 * 交易删光了它也该留着（就像清空聊天记录不该删通讯录）。所以名字和颜色存在<strong>这里</strong>，
 * 一条标签一行；「哪几条记录打了它」存在关联表 {@code trade_record_tag} 里。
 *
 * <p>2026-11-01 之前不是这样：那时候标签是 {@link TradeRecord} 上的一个
 * {@code List<RecordTag>}（{@code @ElementCollection}），用 {@code (record_id, tag_index)}
 * 当主键，名字和颜色直接写在关联行上。结果同一个「追单」在 20 条记录里是 20 行
 * 互不相关的数据 —— 改颜色要改 20 次，也没法回答「我一共有哪些标签」。
 *
 * <h2>为什么名字唯一</h2>
 *
 * <p>数据库侧有 {@code ux_tag_name}。允许同名的话，用户眼里是「一个追单」，
 * 库里却是两行，「筛出所有追单」就会漏掉一半。名字就是标签的身份，
 * 想区分轻重缓急应该用颜色或另起一个名字，不该用两个一模一样的名字。
 *
 * <h2>为什么没有「删除」方法</h2>
 *
 * <p>标签被记录引用时<strong>不允许删</strong>（{@code trade_record_tag.tag_id} 上是
 * {@code ON DELETE RESTRICT}，见 schema.sql）。这条规则故意落在数据库上而不是落在某个
 * service 的 if 里：删除入口将来可能不止一个，约束只有写在 DDL 上才不会漏。
 * 真要删，得先把所有记录上的它摘掉 —— 那是显式动作，不该被一次误点抹掉。
 */
@Getter
@Entity
@Table(name = "trade_tag")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class TradeTag extends BaseEntity {

    /** {@code #rrggbb}。和前端 `<input type="color">` 的取值同一个格式。 */
    private static final Pattern HEX_COLOR = Pattern.compile("^#[0-9a-fA-F]{6}$");

    @Column(nullable = false, length = 64)
    private String name;

    @Column(length = 16)
    private String color;

    private TradeTag(String name, String color) {
        this.name = requireName(name);
        this.color = requireColor(color);
    }

    /**
     * 造一个标签。
     *
     * @param name  标签名，<strong>不能为空或全空白</strong>（空白名在界面上是一块点不到的空格）
     * @param color {@code #rrggbb}，<strong>可以为 null</strong>（表示用界面默认色）
     * @throws IllegalArgumentException 名字为空，或颜色不是 {@code #rrggbb}
     */
    public static TradeTag of(String name, String color) {
        return new TradeTag(name, color);
    }

    /** 改名。**唯一的身份是 id，不是名字** —— 改名不该影响任何已经打过的标签。 */
    public void rename(String name) {
        this.name = requireName(name);
    }

    /** 换颜色。{@code null} = 回到默认色。 */
    public void recolor(String color) {
        this.color = requireColor(color);
    }

    private static String requireName(String name) {
        if (name == null || name.isBlank()) {
            throw new IllegalArgumentException("标签名不能为空");
        }
        String trimmed = name.trim();
        if (trimmed.length() > 64) {
            throw new IllegalArgumentException("标签名最多 64 个字符：" + trimmed);
        }
        return trimmed;
    }

    private static String requireColor(String color) {
        if (color == null || color.isBlank()) {
            return null;
        }
        if (!HEX_COLOR.matcher(color).matches()) {
            throw new IllegalArgumentException("标签颜色必须是 #rrggbb 形式：" + color);
        }
        return color;
    }
}
