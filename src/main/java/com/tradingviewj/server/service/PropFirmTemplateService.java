package com.tradingviewj.server.service;

import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;

import java.util.List;

/**
 * PF 模板的增删改查 —— 一张「价目表」的维护入口。
 *
 * <p><strong>职责边界</strong>：只做存取，加上「模板自身是否自洽」的校验
 * （{@link PropFirmTemplate#define}）。不碰账户、不参与判定；删模板不影响已开出的账户，
 * 因为账户存的是规则副本。
 *
 * <p><strong>{@code stage} 建出来之后不能改</strong>：阶段是链上的一环，改它等于把两个
 * 阶段缝在一起 —— 已有账户的升级路径会静默错位，界面上看不出任何异常。要换阶段就新建。
 */
public interface PropFirmTemplateService {

    /**
     * 新建一套模板。
     *
     * @param name           模板名，例如「LucidFlex 50K EVAL」
     * @param description    自由说明，可空
     * @param stage          用这套模板开出来的账户处于哪个阶段
     * @param nextTemplateId 达标后接哪套模板；{@code null} = 升到尽头
     * @param rules          这个阶段的规则（会原样存进来，账户开户时再复制一份走）
     * @throws IllegalArgumentException 名字/阶段/规则缺失，或阶段与规则互相矛盾
     */
    PropFirmTemplate create(String name, String description, PropFirmStage stage,
                            String nextTemplateId, PropFirmRules rules);

    /**
     * 就地改一套模板：名字、说明、下一套、规则。阶段改不了。
     *
     * <p>校验和 {@link #create} 走同一条路径 —— 更新时把 EVAL 的盈利目标抹掉，同样建不出来。
     *
     * @throws com.tradingviewj.server.exception.BusinessException 模板不存在
     * @throws IllegalArgumentException                            改名后与阶段矛盾
     */
    PropFirmTemplate update(String id, String name, String description,
                            String nextTemplateId, PropFirmRules rules);

    /**
     * 取一套模板。
     *
     * @throws com.tradingviewj.server.exception.BusinessException 模板不存在
     */
    PropFirmTemplate get(String id);

    /** 全部模板，按创建时间升序（列表页的顺序）。 */
    List<PropFirmTemplate> list();

    /**
     * 删一套模板，<strong>只删这一行</strong>。
     *
     * <p>已经用它的规则开出来的账户不受影响。幂等：删一个已经不在的模板不报错。
     */
    void delete(String id);
}
