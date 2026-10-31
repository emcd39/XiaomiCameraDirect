package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct{ HTTP *http.Client }

func New() *Client { return &Client{HTTP: &http.Client{Timeout: 8 * time.Second}} }

func (c *Client) post(ctx context.Context, endpoint, operation, body string) error {
	payload := `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>` +
		body + `</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8; action="http://www.onvif.org/ver20/ptz/wsdl/`+operation+`"`)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ONVIF %s: %w", operation, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ONVIF %s: HTTP %d", operation, resp.StatusCode)
	}
	// SOAP can return a Fault with HTTP 200; don't silently report success.
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("ONVIF %s: malformed XML: %w", operation, err)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "Fault" {
			return fmt.Errorf("ONVIF %s: SOAP fault", operation)
		}
	}
	return nil
}

func (c *Client) Step(ctx context.Context, endpoint, profile, direction string, step float64) error {
	var x, y float64
	switch direction {
	case "left":
		x = -step
	case "right":
		x = step
	case "up":
		y = step
	case "down":
		y = -step
	default:
		return fmt.Errorf("unknown direction %q", direction)
	}
	if math.IsNaN(step) || step <= 0 || step > 1 {
		return fmt.Errorf("invalid step")
	}
	p := escape(profile)
	body := `<tptz:RelativeMove><tptz:ProfileToken>` + p + `</tptz:ProfileToken><tptz:Translation><tt:PanTilt x="` +
		strconv.FormatFloat(x, 'f', 3, 64) + `" y="` + strconv.FormatFloat(y, 'f', 3, 64) + `" space="http://www.onvif.org/ver10/tptz/PanTiltSpaces/TranslationSpaceFov"/>` +
		`</tptz:Translation></tptz:RelativeMove>`
	return c.post(ctx, endpoint, "RelativeMove", body)
}

func (c *Client) Stop(ctx context.Context, endpoint, profile string) error {
	body := `<tptz:Stop><tptz:ProfileToken>` + escape(profile) + `</tptz:ProfileToken><tptz:PanTilt>true</tptz:PanTilt><tptz:Zoom>false</tptz:Zoom></tptz:Stop>`
	return c.post(ctx, endpoint, "Stop", body)
}

func (c *Client) Preset(ctx context.Context, endpoint, profile, token string) error {
	if token == "" || len(token) > 80 {
		return fmt.Errorf("invalid preset token")
	}
	body := `<tptz:GotoPreset><tptz:ProfileToken>` + escape(profile) + `</tptz:ProfileToken><tptz:PresetToken>` + escape(token) + `</tptz:PresetToken></tptz:GotoPreset>`
	return c.post(ctx, endpoint, "GotoPreset", body)
}

func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
