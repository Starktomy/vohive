package vowifihost

import (
	"context"
	"fmt"
	"strings"
	"time"

	swusim "github.com/Starktomy/vowifi-go/engine/sim"
	"github.com/Starktomy/vowifi-go/engine/swu/ikev2"
	"github.com/Starktomy/vowifi-go/runtimehost"
	"github.com/Starktomy/vowifi-go/runtimehost/eventhost"
	"github.com/Starktomy/vowifi-go/runtimehost/identity"
	"github.com/Starktomy/vowifi-go/runtimehost/messaging"
	"github.com/Starktomy/vowifi-go/runtimehost/voicehost"
)

type runtimeStartFunc func(context.Context, runtimehost.StartRequest) (*runtimehost.Instance, error)

type missingSIMProvider struct{}

func (m missingSIMProvider) GetIMSI() (string, error) {
	return "", fmt.Errorf("missing SIM provider")
}
func (m missingSIMProvider) CalculateAKA(rand, autn []byte) (swusim.AKAResult, error) {
	return swusim.AKAResult{}, fmt.Errorf("missing SIM provider")
}
func (m missingSIMProvider) Close() error { return nil }

// buildVoWiFiSIMAdapter prefers an injected SIM adapter (e.g. MBIM Auth AKA for
// modems without SIM logical-channel APDU); otherwise derives one from the
// modem's APDU path (AT/QMI).
func buildVoWiFiSIMAdapter(override runtimehost.SIMAdapter, modem runtimehost.Modem, imsi string) runtimehost.SIMAdapter {
	if override != nil {
		return override
	}
	// 所有后端的 AKA 现由 vohive 注入；缺失说明编排未设置，属调用错误。
	return runtimehost.NewReaderSIMAdapter(missingSIMProvider{})
}

type RuntimeStartRequest struct {
	DeviceID      string
	TraceID       string
	Epoch         uint64
	Prepared      PreparedStart
	Modem         runtimehost.Modem
	Dataplane     runtimehost.DataplanePolicy
	VoiceGateway  *voicehost.Gateway
	DeliveryStore messaging.DeliveryStore
	Dispatch      eventhost.Dispatcher
	BeforeStart   func(context.Context, runtimehost.SessionConfig) error
}

type RuntimeStartResult struct {
	Instance *runtimehost.Instance
	Stale    bool
}

func (m *Manager) SetRuntimeStartForTest(fn runtimeStartFunc) {
	if m == nil {
		return
	}
	m.runtimeStart = fn
}

func (m *Manager) runtimeStarter() runtimeStartFunc {
	if m != nil && m.runtimeStart != nil {
		return m.runtimeStart
	}
	return runtimehost.Start
}

func (m *Manager) StartRuntime(ctx context.Context, req RuntimeStartRequest) (RuntimeStartResult, error) {
	if m == nil {
		return RuntimeStartResult{}, fmt.Errorf("vowifi host manager is nil")
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return RuntimeStartResult{}, fmt.Errorf("vowifi runtime start device_id is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	prepared := req.Prepared.Prepared
	profile := prepared.Profile
	if strings.TrimSpace(profile.IMSI) == "" {
		profile = req.Prepared.Profile
	}
	networkMode := strings.TrimSpace(req.Prepared.StartupState.NetworkMode)
	if networkMode == "" {
		networkMode = strings.TrimSpace(req.Prepared.NetworkMode)
	}

	inst, err := m.runtimeStarter()(ctx, runtimehost.StartRequest{
		Mode:          runtimehost.StartModeMain,
		DeviceID:      deviceID,
		TraceID:       strings.TrimSpace(req.TraceID),
		Profile:       profile,
		Prepared:      &prepared,
		NetworkMode:   networkMode,
		VoiceGateway:  req.VoiceGateway,
		SIM:           buildVoWiFiSIMAdapter(req.Prepared.SIM, req.Modem, prepared.Profile.IMSI),
		Access:        runtimehost.NewModemAccessAdapter(req.Modem),
		Dataplane:     req.Dataplane,
		Proxy:         req.Prepared.Proxy,
		ResponderID:   buildVoWiFiResponderID(prepared),
		IMSRegistrar:  runtimehost.WireIMSRegistrar{},
		DeliveryStore: req.DeliveryStore,
		Dispatch:      req.Dispatch,
		BeforeStart:   req.BeforeStart,
		ShouldRun: func() bool {
			return ctx.Err() == nil && m.ShouldRun(deviceID, req.Epoch)
		},
	})
	if err != nil {
		return RuntimeStartResult{}, err
	}

	inst.AddObserver(runtimehost.ObserverFunc(func(_ context.Context, ev runtimehost.Event) {
		if m.IsCurrentInstance(deviceID, inst) {
			m.BroadcastState(deviceID)
			return
		}
		m.RecordStartupState(deviceID, ev.State)
	}))

	if !m.ClaimStarted(deviceID, req.Epoch, inst) {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = inst.Stop(stopCtx)
		cancel()
		m.ClearStartupStateAndBroadcast(deviceID)
		return RuntimeStartResult{Instance: inst, Stale: true}, nil
	}

	return RuntimeStartResult{Instance: inst}, nil
}

// buildVoWiFiResponderID returns the IDr (ePDG identity) to embed in the first
// IKE_AUTH request. Per 3GPP TS 24.302 §7.2.2.1 the UE MUST send IDr — without
// it T-Mobile US (and most TS-compliant ePDGs) reject with INVALID_SYNTAX.
//
// We use the IMS APN from the prepared carrier session as ID_FQDN. StrongSwan /
// swu_ike.py default to the bare APN ("ims"); the operator-identified APN-FQDN
// form ("ims.apn.epc.mnc<MNC3>.mcc<MCC3>.pub.3gppnetwork.org") is also accepted
// but only matters when the carrier requires it. The bare-APN form is the
// empirically-proven most-compatible choice (see swu_ike.py IDR_MODE=apn default).
//
// Returning the zero-value Identity from BuildIKEAuthInitialPayloads means "omit
// IDr" (legacy behaviour preserved for non-3GPP servers).
func buildVoWiFiResponderID(prepared identity.PreparedSession) ikev2.Identity {
	apn := strings.TrimSpace(prepared.APN)
	if apn == "" {
		// No APN resolved from carrier policy — keep legacy behaviour rather
		// than fabricating an IDr that the ePDG might reject.
		return ikev2.Identity{}
	}
	return ikev2.Identity{
		Type: ikev2.IDFQDN,
		Data: []byte(apn),
	}
}
