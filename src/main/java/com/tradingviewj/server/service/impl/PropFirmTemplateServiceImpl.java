package com.tradingviewj.server.service.impl;

import com.tradingviewj.server.entity.propfirm.PropFirmRules;
import com.tradingviewj.server.entity.propfirm.PropFirmStage;
import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import com.tradingviewj.server.exception.BusinessException;
import com.tradingviewj.server.repository.PropFirmTemplateRepository;
import com.tradingviewj.server.service.PropFirmTemplateService;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.util.List;

/**
 * {@link PropFirmTemplateService} 的唯一实现：把实体工厂方法包一层事务。
 *
 * <p>这里刻意<strong>不做任何计算和判断</strong>：模板是否自洽问实体（{@code define} /
 * {@code redefine}），是否引用了不存在的下一套由将来的调用方自己去查。多一层「顺手校验」
 * 只会让同一条规则有两个落点。
 */
@Service
@RequiredArgsConstructor
public class PropFirmTemplateServiceImpl implements PropFirmTemplateService {

    private final PropFirmTemplateRepository templates;

    @Override
    @Transactional
    public PropFirmTemplate create(String name, String description, PropFirmStage stage,
                                   String nextTemplateId, PropFirmRules rules) {
        return templates.save(PropFirmTemplate.define(name, description, stage, nextTemplateId, rules));
    }

    @Override
    @Transactional
    public PropFirmTemplate update(String id, String name, String description,
                                   String nextTemplateId, PropFirmRules rules) {
        PropFirmTemplate template = get(id);
        template.redefine(name, description, nextTemplateId, rules);
        return templates.save(template);
    }

    @Override
    @Transactional(readOnly = true)
    public PropFirmTemplate get(String id) {
        return templates.findById(id)
                .orElseThrow(() -> new BusinessException("模板不存在: " + id));
    }

    @Override
    @Transactional(readOnly = true)
    public List<PropFirmTemplate> list() {
        return templates.findAllByOrderByCreatedAtAsc();
    }

    @Override
    @Transactional
    public void delete(String id) {
        if (!templates.existsById(id)) {
            return;
        }
        templates.deleteById(id);
    }
}
