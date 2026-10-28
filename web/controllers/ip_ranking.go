package controllers

import (
	"ehang.io/nps/lib/ipstats"
	"net/http"
	"time"
)

func (s *IndexController) Ipranking() {
	s.Ctx.Output.Header("Cache-Control", "private, no-store")
	if s.GetSession("auth") != true || s.GetSession("isAdmin") != true {
		s.Abort("403")
		return
	}
	if s.Ctx.Request.Method != http.MethodGet {
		s.Abort("405")
		return
	}
	if s.GetString("data") != "1" {
		s.SetInfo("IP ranking")
		s.display("index/ipranking")
		return
	}
	snapshot := ipstats.Default.Snapshot(time.Now(), s.GetString("search"))
	total := len(snapshot.Rows)
	offset := s.GetIntNoErr("offset")
	limit := s.GetIntNoErr("limit")
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	end := offset + limit
	if end > total {
		end = total
	}
	s.Data["json"] = map[string]interface{}{"total": total, "rows": snapshot.Rows[offset:end], "connections": snapshot.Connections, "started": snapshot.Started, "window_start": snapshot.WindowStart, "updated": snapshot.Updated}
	s.ServeJSON()
}
