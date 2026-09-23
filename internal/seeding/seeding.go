// Package seeding 价格播种：config.toml 模板 → pricing 表生效行。
// 幂等：同 (model, grp, starts_at) 已存在则跳过；活动价/价格组调整走管理后台（DB 热生效），
// 本模块只在库中缺基础价时补种——保证"零配置起步 → 填配置重启 → 自动可售"。
package seeding

import (
	"database/sql"
	"log"

	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

// SeedAll 全部线 × 全部模型播种 normal + vip 两组（free 线免计费，不播种）
func SeedAll(d *sql.DB, c *config.Cfg) error {
	for i := range c.Lines {
		l := &c.Lines[i]
		if l.Mode == "free" {
			continue
		}
		for j := range l.Models {
			m := &l.Models[j]
			full := config.ModelFullName(l.ID, m.SiteID)
			if l.Mode == "per_token" && !m.Image {
				if err := seedTokenModel(d, l, full, m, "normal"); err != nil {
					return err
				}
				if err := seedTokenModel(d, l, full, m, "vip"); err != nil {
					return err
				}
			} else {
				// 按次 / 按张
				if err := seedCallModel(d, l, full, m, "normal"); err != nil {
					return err
				}
				if err := seedCallModel(d, l, full, m, "vip"); err != nil {
					return err
				}
			}
		}
		// 密钥台账初始行（line + idx 维度）
		for k := range l.Keys {
			if l.Keys[k] == "" {
				continue
			}
			_, _ = d.Exec(
				`INSERT INTO line_keys (line_id, idx, initial_micro, used_micro, dead, updated_ts)
				 VALUES (?,?,?,0,0,0)
				 ON CONFLICT(line_id, idx) DO NOTHING`,
				l.ID, k, l.KeyFaceMicro)
		}
	}
	return nil
}

// seedTokenModel 按量模型播种（三段价 rate10）
func seedTokenModel(d *sql.DB, l *config.Line, full string, m *config.Model, grp string) error {
	in, cache, out := m.InSellRate10, m.CacheSellRate10, m.OutSellRate10
	if grp == "vip" {
		// vip = 普通售价 × VipNum/VipDen（整数运算零精度损失）；倍率未配置（0/0）按 1/1 兜底，防除零 panic
		num, den := l.VipNum, l.VipDen
		if den <= 0 {
			num, den = 1, 1
		}
		in = m.InSellRate10 * num / den
		cache = m.CacheSellRate10 * num / den
		out = m.OutSellRate10 * num / den
	}
	// 按量保本防线（20260919 计费审计新增）：per_token 线的 BreakevenFloor 恒为 0，
	// 此前**完全没有**逐段保本校验——生产已出现亏本配置（图片模型售价 0.1 元 < 成本 0.2 元）。
	// 口径：仅告警，不阻断播种（项目铁律：启动期防线只许告警+跳过，绝不返回错误）。
	// 已存在生效行时不告警（管理台有意调价优先，防线无权否决存量价目）。
	var exist int
	if err := d.QueryRow("SELECT COUNT(*) FROM pricing WHERE model=? AND grp=? AND starts_at=0", full, grp).Scan(&exist); err == nil && exist == 0 {
		if below, why := billing.IsBelowCost("per_token", in, cache, out,
			m.InCostRate10, m.CacheCostRate10, m.OutCostRate10); below {
			log.Printf("[seeding] ⚠️ 亏本告警：%s %s %s（每卖一次亏一次，请到管理台核价）", full, grp, why)
		}
	}
	return insertIfAbsent(d, full, grp, "per_token", 1000, 1000, in, cache, out)
}

// seedCallModel 按次/按张模型播种
func seedCallModel(d *sql.DB, l *config.Line, full string, m *config.Model, grp string) error {
	price := m.PerCallSell
	cost := m.PerCallCost
	if m.Image {
		price = m.PerImageSell
		cost = m.PerImageCost
	}
	if grp == "vip" {
		// 倍率未配置（0/0）按 1/1 兜底，防除零 panic（20260918 aqua 线 crash-loop 根因）
		num, den := l.VipNum, l.VipDen
		if den <= 0 {
			num, den = 1, 1
		}
		price = price * num / den
	}
	// 保本防线（20260919 生产事故修正）：仅当库中确无生效行、且折后售价低于成本时
	// 跳过播种并告警——绝不返回错误阻断启动（存量库中存在管理台调价/历史低价行，
	// 播种防线只防"新播种低价"，无权否决已在售价目；返回错误会导致 SeedAll 失败 → 网关 crash-loop）
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM pricing WHERE model=? AND grp=? AND starts_at=0", full, grp).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil // 已有生效行（管理台改价优先），防线不适用
	}
	if cost > 0 && price < cost {
		log.Printf("[seeding] 警告：%s %s 售价 %d 低于成本 %d，跳过播种（请到管理台核价）", full, grp, price, cost)
		return nil
	}
	return insertIfAbsent(d, full, grp, "per_call", price, 0, 0, 0, 0)
}

// insertIfAbsent 幂等插入（同 model+grp+starts_at=0 已有行则不动——管理后台改价优先）
func insertIfAbsent(d *sql.DB, model, grp, mode string, priceMicro, floor, inR, cacheR, outR int64) error {
	var n int
	if err := d.QueryRow(
		"SELECT COUNT(*) FROM pricing WHERE model=? AND grp=? AND starts_at=0", model, grp).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := d.Exec(
		`INSERT INTO pricing (model, price_micro, starts_at, ends_at, note, grp, mode, floor_micro, in_rate10, cache_rate10, out_rate10)
		 VALUES (?,?,0,NULL,'配置模板播种',?,?,?,?,?,?)`,
		model, priceMicro, grp, mode, floor, inR, cacheR, outR)
	return err
}
