package console

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type PTZConfig struct {
	Enabled bool `json:"enabled"`
}

type Config struct {
	Listen           string    `json:"listen"`
	ListenFallback   bool      `json:"listen_fallback"`
	Mock             bool      `json:"mock"`
	GatewayURL       string    `json:"gateway_url"`
	TokenFile        string    `json:"token_file"`
	RTSPURLFile      string    `json:"rtsp_url_file"` // Legacy config key; ignored so streams stay paired with managed inventory selection.
	AllowedRTSPHosts []string  `json:"allowed_rtsp_hosts"`
	FFmpegPath       string    `json:"ffmpeg_path"`
	RecordDir        string    `json:"record_dir"`
	CameraAddress    string    `json:"camera_address"`
	PTZ              PTZConfig `json:"ptz"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:18880"
	}
	if c.GatewayURL == "" {
		c.GatewayURL = "http://127.0.0.1:49320"
	}
	if c.FFmpegPath == "" {
		c.FFmpegPath = "ffmpeg"
	}
	if c.RecordDir == "" {
		c.RecordDir = "recordings"
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if !filepath.IsAbs(c.RecordDir) {
		c.RecordDir, err = filepath.Abs(c.RecordDir)
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be loopback host:port: %w", err)
	}
	if !isLoopback(host) {
		return fmt.Errorf("listen must be loopback only (127.0.0.1 or ::1)")
	}
	u, err := url.Parse(c.GatewayURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !isLoopback(u.Hostname()) || u.Port() == "" {
		return fmt.Errorf("gateway_url must be http://127.0.0.1:PORT (no path, credentials or query)")
	}
	if c.CameraAddress != "" && net.ParseIP(c.CameraAddress) == nil {
		return fmt.Errorf("camera_address must be an IP address")
	}
	for _, h := range c.AllowedRTSPHosts {
		if h == "" || strings.ContainsAny(h, "/:@ ") {
			return fmt.Errorf("allowed_rtsp_hosts must contain hostnames/IPs without ports or URL syntax")
		}
	}
	return nil
}

func isLoopback(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
