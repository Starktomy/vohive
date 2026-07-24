package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Starktomy/vohive/internal/db"
	"github.com/gin-gonic/gin"
)

// patchCardPolicyForDevice 解析设备当前 ICCID，对 card_policies 行执行原地修改并落库。
// mutate 在 resolve 后的副本上改字段（source 会被强制为 "user"）。
// applied=false 且 err=nil 表示设备当前无 ICCID（离线/未识别），跳过落库。
func (s *Server) patchCardPolicyForDevice(deviceID string, mutate func(*db.CardPolicy)) (iccid string, applied bool, err error) {
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		return "", false, fmt.Errorf("设备未找到")
	}
	iccid = worker.CurrentICCID()
	if iccid == "" {
		return "", false, nil
	}
	p, err := db.ResolveCardPolicy(iccid)
	if err != nil {
		return iccid, false, fmt.Errorf("获取卡策略失败: %w", err)
	}
	mutate(&p)
	p.Source = "user"
	db.NormalizeCardPolicy(&p)
	if err := db.UpsertCardPolicy(p); err != nil {
		return iccid, false, fmt.Errorf("保存卡策略失败: %w", err)
	}
	return iccid, true, nil
}

func (s *Server) handleGetCardPolicy(c *gin.Context) {
	iccid := c.Param("iccid")
	pol, err := db.GetCardPolicy(iccid)
	if errors.Is(err, db.ErrCardPolicyNotFound) {
		// 未建档则返回默认模板（不落库，读端点保持只读语义）
		c.JSON(http.StatusOK, db.DefaultCardPolicy(iccid))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pol)
}

func (s *Server) handleListCardPolicies(c *gin.Context) {
	var out []db.CardPolicy
	if db.DB != nil {
		if err := db.DB.Order("updated_at desc").Find(&out).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"policies": out})
}

func (s *Server) handlePutCardPolicy(c *gin.Context) {
	iccid := c.Param("iccid")
	var req struct {
		NetworkEnabled *bool  `json:"network_enabled"`
		VoWiFiEnabled  *bool  `json:"vowifi_enabled"`
		IPVersion      string `json:"ip_version"`
		APN            string `json:"apn"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 查出当前策略（查不到则用默认值）
	pol, err := db.GetCardPolicy(iccid)
	if err != nil {
		pol = db.DefaultCardPolicy(iccid)
	}

	if req.NetworkEnabled != nil {
		pol.NetworkEnabled = *req.NetworkEnabled
	}
	if req.VoWiFiEnabled != nil {
		pol.VoWiFiEnabled = *req.VoWiFiEnabled
	}
	if req.IPVersion != "" {
		pol.IPVersion = req.IPVersion
	}
	if req.APN != "" {
		pol.APN = req.APN
	}
	pol.Source = "user"

	if err := db.UpsertCardPolicy(pol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 同步 worker.Config：PUT 是"直接覆盖卡策略"，前端可能一次设多个字段。
	// 三个 SetWorker*Policy 内部互斥（开 VoWiFi ⇒ airplane+network=false，开网络 ⇒ 其余两项=false），
	// 不能简单按顺序调用：后一个会覆盖前一个的互斥副作用。改为按"最终生效模式"调一次：
	//   - VoWiFi=true（用户意图） → SetWorkerVoWiFiPolicy（顺便清 network=false，airplane=true 表征"VoWiFi 接管射频"）
	//   - 否则若 airplane=true    → SetWorkerAirplanePolicy（清 VoWiFi+network）
	//   - 否则若 network=true     → SetWorkerNetworkPolicy（清 VoWiFi+airplane）
	//   - 三个都关                  → 同步 airplane=false 走网络停用链
	// 没有匹配 worker（设备未识别/离线）时静默跳过——worker 上线时仍会走 resolveAndApplyPolicy 投影。
	if w := s.pool.WorkerByICCID(iccid); w != nil {
		deviceID := w.ID
		switch {
		case pol.VoWiFiEnabled:
			s.pool.SetWorkerVoWiFiPolicy(deviceID, true)
		case pol.AirplaneEnabled:
			s.pool.SetWorkerAirplanePolicy(deviceID, true)
		case pol.NetworkEnabled:
			s.pool.SetWorkerNetworkPolicy(deviceID, true, pol.IPVersion, pol.APN)
		default:
			// 三模式全关：按"清零"语义调用，仅同步 airplane=false（关闭飞行回退的兜底）。
			s.pool.SetWorkerAirplanePolicy(deviceID, false)
			s.pool.SetWorkerVoWiFiPolicy(deviceID, false)
			s.pool.SetWorkerNetworkPolicy(deviceID, false, pol.IPVersion, pol.APN)
		}
	}

	c.JSON(http.StatusOK, pol)
}
