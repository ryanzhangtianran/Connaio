//go:build !darwin

package app

import "testing"

func TestDetailTextUnwrapsPillsAndModeBadges(t *testing.T) {
	for in, want := range map[string]string{pill("good", "86 ms"): "86 ms", dot("good", "已连接"): "● 已连接", dot("gray", "未开启"): "○ 未开启", "R": "规则判定", "G": "全局代理", "香港": "香港", "": ""} {
		if got := detailText(in); got != want {
			t.Errorf("detailText(%q) = %q, want %q", in, got, want)
		}
	}
}
