package com.tradingviewj.server.entity.record;

import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;

/**
 * 用户给一笔交易记录打的自定义标签。
 *
 * <p>名字和颜色都由用户定，服务端不当老师：只要求名字非空、颜色是 {@code #rrggbb}。
 * 颜色跟着标签走而不是「标签名 → 主题色」查表 —— 同一个词在不同人眼里轻重不同
 * （「追单」有人标红有人标黄），颜色是用户自己的记号，存在标签上才能做到
 * 「打过的标签下次点一下就还原成一模一样的样子」。
 */
@Getter
@Embeddable
@NoArgsConstructor(access = AccessLevel.PROTECTED)
public class RecordTag {

    @Column(name = "name", nullable = false)
    private String name;

    @Column(name = "color")
    private String color;

    public RecordTag(String name, String color) {
        if (name == null || name.isBlank()) {
            throw new IllegalArgumentException("标签名不能为空");
        }
        this.name = name;
        this.color = color;
    }
}
