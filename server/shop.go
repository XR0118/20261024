package main

import "fmt"

// 皮肤 id 与前端 web/src/game/skin.ts 的 SkinId 严格一致（跨端协议，改名必须两端同步）
const (
	skinBean  = "bean"
	skinWave  = "wave"
	skinPrice = 20 // 与前端 SKIN_PRICE 严格一致
)

// 三种决策结果：买了 / 差多少 / 直接切换
const (
	kindBought   = "bought"
	kindShort    = "short"
	kindSwitched = "switched"
)

// SkinShop 语音造型商店的纯决策：只看档案快照，不碰存储——
// 扣费与落库由 Store 负责（决策与执行分离，便于无 IO 测试）
type SkinShop struct {
	Price int
}

type ShopOutcome struct {
	Kind         string
	Skin         string // 本操作的目标造型
	Short        int    // kindShort 时的差额
	BalanceAfter int    // kindBought 时的余额
}

func (s SkinShop) Decide(owned []string, active string, balance int) ShopOutcome {
	target := skinWave
	if active == skinWave {
		target = skinBean
	}
	for _, owned2 := range owned {
		if owned2 == target {
			return ShopOutcome{Kind: kindSwitched, Skin: target}
		}
	}
	if balance >= s.Price {
		return ShopOutcome{Kind: kindBought, Skin: target, BalanceAfter: balance - s.Price}
	}
	return ShopOutcome{Kind: kindShort, Skin: target, Short: s.Price - balance}
}

// skinName 播报用中文名
func skinName(id string) string {
	if id == skinWave {
		return "声波精灵"
	}
	return "跳跳豆"
}

// shopLine 播报文案：购买确认带剩余余额，差额给可行动的下一步
func shopLine(o ShopOutcome) string {
	switch o.Kind {
	case kindBought:
		return fmt.Sprintf("%s到手！余额还剩 %d 金币，穿上试试！", skinName(o.Skin), o.BalanceAfter)
	case kindShort:
		return fmt.Sprintf("还差 %d 金币解锁%s，跑一局就有了！", o.Short, skinName(o.Skin))
	default:
		return fmt.Sprintf("换上%s！", skinName(o.Skin))
	}
}
