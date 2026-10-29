package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.propfirm.PropFirmTemplate;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface PropFirmTemplateRepository extends JpaRepository<PropFirmTemplate, String> {

    List<PropFirmTemplate> findAllByOrderByCreatedAtAsc();
}
