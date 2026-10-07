package main

import (
	"encoding/json"
	"fmt"
)

// Badge filenames are observed DCInside markers. No production classifier is
// stubbed: the real collector must interpret the HTML in gallog_icon.
func badgeComments() ([]byte, error) {
	groups := []struct{ name, nick, icon string }{
		{"고닉", "20", "fix_nik.gif"},
		{"비고닉", "00", "nik.gif"},
		{"주매니저", "20", "fix_managernik.gif"},
		{"부매니저", "00", "sub_managernik.gif"},
		{"깡계", "20", "fix_newnik.gif"},
		{"유동닉", "00", ""},
	}
	rows := make([]map[string]any, 0, 12)
	for groupIndex, group := range groups {
		for person := 0; person < 2; person++ {
			user, ip, icon := fmt.Sprintf("badge-%d-%d", groupIndex, person), "", ""
			if group.icon == "" {
				user, ip = "", fmt.Sprintf("192.0.2.%d", person+1)
			} else {
				icon = `<img src="https://nstatic.dcinside.com/dc/w/images/` + group.icon + `" alt="닉네임 배지">`
			}
			rows = append(rows, map[string]any{"no": fmt.Sprint(len(rows) + 1), "parent": "1", "name": fmt.Sprintf("%s 참가자 %d", group.name, person+1), "user_id": user, "ip": ip, "reg_date": "2026.10.06 12:00:00", "nicktype": group.nick, "gallog_icon": icon, "memo": "참여합니다", "depth": 0, "c_no": 0, "is_delete": "0", "del_yn": "N"})
		}
	}
	return json.Marshal(map[string]any{"total_cnt": len(rows), "comment_cnt": 0, "comments": rows, "pagination": "<em>1</em>", "allow_reply": 1})
}
