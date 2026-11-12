package console

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type PTZResult struct {
	Outcome         string `json:"outcome"`
	Direction       string `json:"direction"`
	HTTPWriteStatus string `json:"http_write_status"`
	Reply113        string `json:"reply_0x113"`
	Motion          string `json:"motion"`
	Note            string `json:"note"`
}

type PTZ struct {
	mu   sync.Mutex
	busy bool
	last PTZResult
}

func (p *PTZ) Busy() bool      { p.mu.Lock(); defer p.mu.Unlock(); return p.busy }
func (p *PTZ) Last() PTZResult { p.mu.Lock(); defer p.mu.Unlock(); return p.last }

func (p *PTZ) RunOnce(c Config, gateway *Gateway, cameraID, direction string) (PTZResult, error) {
	p.mu.Lock()
	if p.busy {
		p.mu.Unlock()
		return PTZResult{}, errors.New("PTZ operation already in progress")
	}
	p.busy = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.busy = false; p.mu.Unlock() }()

	if direction != "left" && direction != "right" && direction != "up" && direction != "down" {
		return PTZResult{}, errors.New("invalid PTZ direction")
	}
	if c.Mock {
		result := PTZResult{
			Outcome: "simulated", Direction: direction, HTTPWriteStatus: "simulated_only",
			Reply113: "not_measured", Motion: "not_measured",
			Note: "Mock only. No gateway request, camera session or motor command.",
		}
		p.setLast(result)
		return result, nil
	}
	if !c.PTZ.Enabled {
		return p.fail(direction, errors.New("live PTZ is disabled in config"))
	}
	if gateway == nil {
		return p.fail(direction, errors.New("gateway is unavailable"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := gateway.PrepareCamera(ctx, cameraID); err != nil {
		return p.fail(direction, err)
	}
	writeStatus, err := gateway.Step(ctx, cameraID, direction)
	if err != nil {
		return p.fail(direction, err)
	}
	result := PTZResult{
		Outcome: "sent", Direction: direction, HTTPWriteStatus: writeStatus,
		Reply113: "not_measured", Motion: "not_measured",
		Note: "Gateway accepted one PTZ step (write_status=sent). Physical motion was not verified by the API response.",
	}
	p.setLast(result)
	return result, nil
}

func (p *PTZ) fail(direction string, err error) (PTZResult, error) {
	result := PTZResult{
		Outcome: "failed", Direction: direction, HTTPWriteStatus: "unknown",
		Reply113: "not_measured", Motion: "not_measured", Note: err.Error(),
	}
	p.setLast(result)
	return result, fmt.Errorf("%s", err.Error())
}

func (p *PTZ) setLast(result PTZResult) {
	p.mu.Lock()
	p.last = result
	p.mu.Unlock()
}
