package server

import (
	"errors"
	"net/http"

	"github.com/boltguo/sbm/internal/traffic"
)

func (s *Server) trafficHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	granularity := query.Get("granularity")
	if granularity == "" {
		granularity = "day"
	}
	var result traffic.HistoryResponse
	var err error
	scope := query.Get("scope")
	if s.Traffic.UsesVnStat() {
		if scope != "" && scope != traffic.EntryNetworkScope {
			writeError(w, 400, "vnStat 历史仅统计 SBM 入口")
			return
		}
		result, err = s.Traffic.NetworkHistory(r.Context(), traffic.EntryNetworkScope, granularity, query.Get("from"), query.Get("to"))
	} else {
		result, err = s.Traffic.History(r.Context(), granularity, query.Get("from"), query.Get("to"))
	}

	switch {
	case errors.Is(err, traffic.ErrHistoryRange):
		writeError(w, http.StatusBadRequest, "流量历史查询范围无效")
	case errors.Is(err, traffic.ErrHistoryDisabled):
		writeError(w, http.StatusServiceUnavailable, "流量历史记录未启用")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "读取流量历史失败")
	default:
		writeJSON(w, http.StatusOK, result)
	}
}
