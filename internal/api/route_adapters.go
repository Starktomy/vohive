package api

import (
	"net/http"
	"strings"

	"github.com/Starktomy/vohive/internal/db"
	"github.com/Starktomy/vohive/pkg/logger"
	"github.com/gin-gonic/gin"
)

type enabledPatchRequest struct {
	Enabled *bool `json:"enabled"`
}

type networkPatchRequest struct {
	Enabled   *bool  `json:"enabled"`
	IPVersion string `json:"ip_version"`
	APN       string `json:"apn"`
}

func (s *Server) handleDeviceNetworkPatch(c *gin.Context) {
	var req networkPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "enabled 为必填项"})
		return
	}

	deviceID := deviceIDParam(c)

	if *req.Enabled {
		// 落库：network_enabled=true + ip_version + apn（APN/IP 供下次连接生效）
		ipVersion := strings.TrimSpace(req.IPVersion)
		apn := strings.TrimSpace(req.APN)
		iccid, _, _ := s.patchCardPolicyForDevice(deviceID, func(p *db.CardPolicy) {
			p.NetworkEnabled = true
			if ipVersion != "" {
				p.IPVersion = ipVersion
			}
			p.APN = apn
		})
		// 同步 w.Config，使概览读到最新值（QMI APN 在下次连接时生效）
		if iccid != "" {
			s.pool.SetWorkerNetworkPolicy(deviceID, true, ipVersion, apn)
		}
		s.handleDeviceMgmtStartNetwork(c)
		return
	}

	// enabled=false：落库 network_enabled=false
	s.patchCardPolicyForDevice(deviceID, func(p *db.CardPolicy) {
		p.NetworkEnabled = false
	})
	s.handleDeviceMgmtStopNetwork(c)
}

func (s *Server) handleDeviceVoWiFiPatch(c *gin.Context) {
	var req enabledPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "enabled 为必填项"})
		return
	}

	deviceID := deviceIDParam(c)

	if *req.Enabled {
		// 落库：仅置 vowifi_enabled=true。不碰 airplane_enabled——它是用户的纯飞行
		// 意图，作为关闭 VoWiFi 后的回退依据；VoWiFi 接管射频由运行时投影派生。
		s.patchCardPolicyForDevice(deviceID, vowifiEnablePolicyMutation)
		// 同步 w.Config，使概览即时切到 VoWiFi 模式面板（EnableVoWiFi 不碰 Config）。
		s.pool.SetWorkerVoWiFiPolicy(deviceID, true)
		// EnableVoWiFi 走 SWU tunnel 建立（默认 8s+ I/O 超时 + IKE 协议握手）会同步阻塞
		// PATCH 响应，前端一直转圈。期间用户再点 disable 会被 InvalidateRuntime 把正在
		// 启动的 attach 干掉，回 500 "runtime start canceled"——但这只是 cancel 副作用，
		// 不是用户语义上的失败。改为 fire-and-forget：立即 200，状态走 SSE 推流；失败/成功
		// 都从概览（vowifi_active）可见；错误日志进 journal 方便事后排查。
		go func(id string) {
			if err := s.pool.EnableVoWiFi(id); err != nil {
				logger.Warn("VoWiFi 后台启用失败", "device", id, "err", err)
			}
		}(deviceID)
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "VoWiFi 已提交启用，状态见概览",
			"device":  deviceID,
		})
		return
	}

	// 落库：仅清 vowifi_enabled=false，保留 airplane_enabled（用户飞行意图）。
	// 关闭 VoWiFi 后 DisableVoWiFi 会按当前卡策略重投影：之前是飞行则回飞行，否则回在线。
	s.patchCardPolicyForDevice(deviceID, vowifiDisablePolicyMutation)
	s.pool.SetWorkerVoWiFiPolicy(deviceID, false)
	s.handleVoWiFiDisable(c)
}

// vowifiEnablePolicyMutation 开 VoWiFi 的落库副作用：只置 vowifi，飞行意图保持不变。
func vowifiEnablePolicyMutation(p *db.CardPolicy) { p.VoWiFiEnabled = true }

// vowifiDisablePolicyMutation 关 VoWiFi 的落库副作用：只清 vowifi，保留用户飞行意图以便回退。
func vowifiDisablePolicyMutation(p *db.CardPolicy) { p.VoWiFiEnabled = false }
