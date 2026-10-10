package model

import (
	"testing"
	"time"
)

// TestNextDay0400 风控冷却的检查点必须是「次日 04:00」。
// 与看门狗的额度恢复检查点同一口径，避免各写一份时间算出岔。
func TestNextDay0400(t *testing.T) {
	loc := time.Local
	cases := []struct {
		now  time.Time
		want time.Time
	}{
		// 凌晨 2 点 → 当天 04:00
		{time.Date(2026, 10, 10, 2, 0, 0, 0, loc), time.Date(2026, 10, 10, 4, 0, 0, 0, loc)},
		// 上午 10 点 → 次日 04:00
		{time.Date(2026, 10, 10, 10, 0, 0, 0, loc), time.Date(2026, 10, 11, 4, 0, 0, 0, loc)},
		// 恰好 04:00 → 次日（不能落在当前或过去）
		{time.Date(2026, 10, 10, 4, 0, 0, 0, loc), time.Date(2026, 10, 11, 4, 0, 0, 0, loc)},
		// 深夜 23:30 → 次日 04:00
		{time.Date(2026, 10, 10, 23, 30, 0, 0, loc), time.Date(2026, 10, 11, 4, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		got := nextDay0400(c.now)
		if !got.Equal(c.want) {
			t.Errorf("nextDay0400(%v)=%v want %v", c.now, got, c.want)
		}
		if !got.After(c.now) {
			t.Errorf("冷却检查点必须晚于当前时间: now=%v got=%v", c.now, got)
		}
	}
}

// TestNextDay0400FarLongerThanGenericCooldown 风控冷却要显著长于通用冷却。
// 通用路径是 cooldown-seconds（线上 600 秒）；若风控也用那个值，号会在
// 10 分钟后放出来、再撞一次、再冷却——循环浪费配额。
func TestNextDay0400FarLongerThanGenericCooldown(t *testing.T) {
	now := time.Now()
	generic := 600 * time.Second
	gap := nextDay0400(now).Sub(now)
	if gap <= generic*2 {
		t.Fatalf("风控冷却应远长于通用冷却 %v，实际 %v", generic, gap)
	}
}
