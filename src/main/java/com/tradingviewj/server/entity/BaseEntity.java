package com.tradingviewj.server.entity;

import jakarta.persistence.Column;
import jakarta.persistence.Id;
import jakarta.persistence.MappedSuperclass;
import jakarta.persistence.PrePersist;
import jakarta.persistence.PreUpdate;
import lombok.Getter;

import java.time.Instant;
import java.util.UUID;

/**
 * 所有实体的公共部分：主键 + 创建/更新时间。
 *
 * <p>主键是 <strong>UUID 字符串</strong>，不是自增数字。三个理由：
 *
 * <ol>
 *   <li>SQLite 的自增列要额外写 {@code AUTOINCREMENT}，而 Hibernate 的 IDENTITY 策略在
 *       SQLite 方言下要回读 {@code last_insert_rowid()}，多一层不确定；UUID 是纯应用侧生成，
 *       一次 insert 就完事。</li>
 *   <li>复盘里的东西天然是「先有 id 再落库」：图形画下去的那一刻 id 就广播给前端了，
 *       不可能等数据库発号。用 UUID 让「内存里的对象」和「库里的行」是同一个 id。</li>
 *   <li>合并/导入时不会撞号。</li>
 * </ol>
 *
 * <p>需要业务本身有天然主键的实体（例如 {@link Symbol} 的代码）可以调
 * {@link #useId(String)} 换掉，所以它是 {@code protected} 的 —— 只给自己人用。
 */
@Getter
@MappedSuperclass
public abstract class BaseEntity {

    @Id
    @Column(nullable = false, updatable = false, length = 64)
    private String id = UUID.randomUUID().toString();

    /** 创建时间。一旦写入不再改动。 */
    @Column(name = "created_at", nullable = false, updatable = false)
    private Instant createdAt;

    /** 最后修改时间，由 {@link #touchOnUpdate()} 维护。 */
    @Column(name = "updated_at", nullable = false)
    private Instant updatedAt;

    /** 用业务主键替换掉默认生成的 UUID（只有继承者自己该调）。 */
    protected void useId(String businessId) {
        this.id = businessId;
    }

    @PrePersist
    void touchOnCreate() {
        Instant now = Instant.now();
        if (createdAt == null) {
            createdAt = now;
        }
        updatedAt = now;
    }

    @PreUpdate
    void touchOnUpdate() {
        updatedAt = Instant.now();
    }
}
