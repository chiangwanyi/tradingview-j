package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface PropFirmAccountRepository extends JpaRepository<PropFirmAccount, String> {

    List<PropFirmAccount> findByStatus(PropFirmAccount.Status status);

    List<PropFirmAccount> findByTemplateId(String templateId);
}
