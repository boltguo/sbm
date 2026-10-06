package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/boltguo/sbm/internal/panelupdate"
)

func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil || s.Releases == nil {
		writeError(w, http.StatusServiceUnavailable, "当前运行环境不支持面板更新")
		return
	}
	var input struct{}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式无效")
		return
	}
	status, err := s.latestUpdate(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法从 GitHub 获取最新版本")
		return
	}
	if !status.UpdateAvailable {
		writeError(w, http.StatusConflict, "当前已是最新版本")
		return
	}
	if !status.CanInstall {
		writeError(w, http.StatusConflict, "当前运行环境不支持面板更新")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.Updater.Start(ctx, status.LatestVersion); err != nil {
		if errors.Is(err, panelupdate.ErrBusy) {
			writeError(w, http.StatusConflict, "已有面板更新正在进行")
		} else {
			log.Printf("启动面板更新失败：%v", err)
			writeError(w, http.StatusServiceUnavailable, "无法启动面板更新，请查看服务器日志")
		}
		return
	}
	log.Printf("audit event=panel-update target=%s", status.LatestVersion)
	writeJSON(w, http.StatusAccepted, panelupdate.Status{State: "running", Phase: "queued", TargetVersion: status.LatestVersion, UpdatedAt: time.Now().UTC()})
}

func (s *Server) updateProgress(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeJSON(w, http.StatusOK, panelupdate.Status{State: "idle"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	status, err := s.Updater.Status(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取面板更新状态")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
