package com.tradingviewj.server.repository;

import com.tradingviewj.server.entity.propfirm.PropFirmAccount;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.List;

public interface PropFirmAccountRepository extends JpaRepository<PropFirmAccount, String> {

    List<PropFirmAccount> findByStatus(PropFirmAccount.Status status);

    List<PropFirmAccount> findByTemplateId(String templateId);

    /** 全部账户，新建的在前（前端列表的顺序）。 */
    List<PropFirmAccount> findAllByOrderByStartedAtDesc();

    /** 某个状态的账户，新建的在前。 */
    List<PropFirmAccount> findByStatusOrderByStartedAtDesc(PropFirmAccount.Status status);
}
