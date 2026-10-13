package main

import (
	"context"
	"encoding/base64"
	"errors"
	"time"

	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/component/stability"
	"github.com/metacubex/mihomo/tunnel"
)

type StabilityParams struct {
	NetworkKey string `json:"networkKey"`
	Online     bool   `json:"online"`
	Group      string `json:"group"`
	Node       string `json:"node"`
	Paused     bool   `json:"paused"`
	PowerSave  bool   `json:"powerSave"`
	Debug      *bool  `json:"debug"`
}

func init() {
	outboundgroup.OnStableRouteChange = func() { safeGoDetached("stableRoute", refreshRoute) }
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

func setStabilityMode(params *StabilityParams) error {
	g, err := stabilityGroup(params.Group)
	if err != nil {
		return err
	}
	g.Configure(params.Paused, params.PowerSave)
	j, err := outboundgroup.StabilityJournal()
	if err != nil {
		return err
	}
	if params.Debug != nil {
		j.Debug(*params.Debug)
	}
	return nil
}

func probeStabilityCandidate(params *StabilityParams) (stability.Sample, error) {
	g, err := stabilityGroup(params.Group)
	if err != nil {
		return stability.Sample{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return g.Probe(ctx, params.Node, true, false)
}

func markStabilityFault(params *StabilityParams) error {
	if _, err := stabilityGroup(params.Group); err != nil {
		return err
	}
	j, err := outboundgroup.StabilityJournal()
	if err != nil {
		return err
	}
	return j.Record(stability.Event{Event: "user_mark", Group: j.ID("group:" + params.Group)})
}

func exportStabilityDiagnostics() (map[string]any, error) {
	j, err := outboundgroup.StabilityJournal()
	if err != nil {
		return nil, err
	}
	data, err := j.Export()
	if err != nil {
		return nil, err
	}
	return map[string]any{"size": len(data), "base64": base64.StdEncoding.EncodeToString(data)}, nil
}
