package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var cameraID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

type Camera struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Model    string  `json:"model,omitempty"`
	RTSPURL  string  `json:"rtsp_url"`
	ONVIFURL string  `json:"onvif_url,omitempty"`
	Profile  string  `json:"profile,omitempty"`
	PTZ      bool    `json:"ptz"`
	Step     float64 `json:"step,omitempty"`
}

type Config struct {
	Listen  string   `json:"listen"`
	Cameras []Camera `json:"cameras"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return c, errors.New("config contains trailing data")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8099"
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ids := map[string]bool{}
	if len(c.Cameras) == 0 {
		return errors.New("configure at least one camera")
	}
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		if !cameraID.MatchString(cam.ID) {
			return fmt.Errorf("camera %d: invalid id (use lowercase letters, digits, underscore)", i)
		}
		if ids[cam.ID] {
			return fmt.Errorf("duplicate camera id: %s", cam.ID)
		}
		ids[cam.ID] = true
		if strings.TrimSpace(cam.Name) == "" {
			return fmt.Errorf("%s: name is required", cam.ID)
		}
		u, err := url.Parse(cam.RTSPURL)
		if err != nil || u.Scheme != "rtsp" || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("%s: rtsp_url must be rtsp://host[:port]/stream without credentials (use protected network)", cam.ID)
		}
		if cam.PTZ {
			v, err := url.Parse(cam.ONVIFURL)
			if err != nil || v.Scheme != "http" || v.Hostname() == "" || v.User != nil || v.RawQuery != "" || v.Fragment != "" {
				return fmt.Errorf("%s: ptz requires an http://host:port/onvif/ptz_service URL", cam.ID)
			}
			if !strings.HasSuffix(v.Path, "/onvif/ptz_service") {
				return fmt.Errorf("%s: unsupported onvif URL path", cam.ID)
			}
		} else if cam.ONVIFURL != "" {
			return fmt.Errorf("%s: onvif_url only allowed with ptz=true", cam.ID)
		}
		if cam.Step == 0 {
			cam.Step = 0.08
		}
		if cam.Step <= 0 || cam.Step > 1 {
			return fmt.Errorf("%s: step must be > 0 and <= 1", cam.ID)
		}
		if cam.Profile == "" {
			cam.Profile = cam.ID
		}
	}
	return nil
}

func Loopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	return net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
