package main

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/component/stability"
	"github.com/metacubex/mihomo/tunnel"
)

type StabilityParams struct {
	NetworkKey             string `json:"networkKey"`
	Online                 bool   `json:"online"`
	Group                  string `json:"group"`
	Node                   string `json:"node"`
	Paused                 bool   `json:"paused"`
	PowerSave              bool   `json:"powerSave"`
	Debug                  *bool  `json:"debug"`
	NotificationGeneration uint64 `json:"notificationGeneration"`
}

var stabilityNotificationMu sync.Mutex
var stabilityNotificationGeneration uint64

func init() {
	stability.RegisterBusinessAnomalyObserver(tunnel.StabilityBusinessAnomaly)
	outboundgroup.OnStableRouteChange = func() {
		sendMessage(Message{Type: StabilityMessage, Data: true})
	}
}

func stabilityGroup(name string) (*outboundgroup.Stable, error) {
	p := tunnel.AllProxies()[name]
	if p == nil {
		return nil, errors.New("stability group not found")
	}
	g, ok := p.Adapter().(*outboundgroup.Stable)
	if !ok {
		return nil, errors.New("not a stability group")
	}
	return g, nil
}

func getStabilityState() []map[string]any {
	states := []map[string]any{}
	for _, p := range tunnel.AllProxies() {
		if g, ok := p.Adapter().(*outboundgroup.Stable); ok {
			states = append(states, g.Snapshot())
		}
	}
	return states
}

func handleStability(call *MethodCall, response MethodResponse) bool {
	switch call.Method {
	case getStabilityStateMethod:
		response.success(getStabilityState())
	case exportStabilityDiagnosticsMethod:
		j, err := outboundgroup.StabilityJournal()
		if err != nil {
			response.failure("stability_error", "diagnostics unavailable", nil)
			return true
		}
		states := map[string]stability.Snapshot{}
		for _, p := range tunnel.AllProxies() {
			if g, ok := p.Adapter().(*outboundgroup.Stable); ok {
				id, state := g.DiagnosticState()
				states[id] = state
			}
		}
		data, err := j.Export(states)
		if err != nil {
			response.failure("stability_error", "diagnostic export failed", nil)
		} else {
			response.success(map[string]any{"size": len(data), "base64": base64.StdEncoding.EncodeToString(data)})
		}
	case stabilityNetworkChangedMethod, setStabilityModeMethod, probeStabilityCandidateMethod, markStabilityFaultMethod:
		params := StabilityParams{}
		if !decodeMethodArguments(call, response, &params) {
			return true
		}
		if call.Method == stabilityNetworkChangedMethod {
			stabilityNotificationMu.Lock()
			defer stabilityNotificationMu.Unlock()
			if params.NotificationGeneration > 0 && params.NotificationGeneration <= stabilityNotificationGeneration {
				response.success(true)
				return true
			}
			stabilityNotificationGeneration = params.NotificationGeneration
			tunnel.StabilityNetworkChanged(params.NetworkKey, params.Online)
			response.success(true)
			return true
		}
		g, err := stabilityGroup(params.Group)
		if err != nil {
			response.failure("stability_error", err.Error(), nil)
			return true
		}
		switch call.Method {
		case setStabilityModeMethod:
			g.Configure(params.Paused, params.PowerSave)
			if params.Debug != nil {
				j, _ := outboundgroup.StabilityJournal()
				j.Debug(*params.Debug)
			}
			response.success(true)
		case probeStabilityCandidateMethod:
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			sample, err := g.Probe(ctx, params.Node, true, false)
			if err != nil {
				response.failure("stability_error", "candidate verification failed", nil)
			} else {
				response.success(sample)
			}
		case markStabilityFaultMethod:
			j, _ := outboundgroup.StabilityJournal()
			j.MarkBusiness(j.ID("group:" + params.Group))
			_ = j.Record(stability.Event{Event: "user_mark", Group: j.ID("group:" + params.Group)})
			response.success(true)
		}
	default:
		return false
	}
	return true
}
