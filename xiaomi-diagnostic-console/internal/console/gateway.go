package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const maxGatewayResponse = 2 << 20

type Gateway struct {
	cfg              Config
	client           *http.Client
	selectionMu      sync.Mutex
	mu               sync.RWMutex
	inventorySummary InventorySummary
	cameras          map[string]gatewayCamera
	selectedCameraID string
	inventoryFetched time.Time
}

type gatewayCamera struct {
	option     CameraOption
	model      string
	rtspURL    string
	videoState string
}

type CameraOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type InventorySummary struct {
	Loaded             bool           `json:"loaded"`
	CameraCount        int            `json:"camera_count"`
	C201Count          int            `json:"c201_count"`
	StreamURLsDetected int            `json:"stream_urls_detected"`
	C201Cameras        []CameraOption `json:"c201_cameras"`
	SelectedCameraID   string         `json:"selected_camera_id,omitempty"`
	SessionReady       bool           `json:"session_ready"`
	SessionState       string         `json:"session_state,omitempty"`
	FetchedAt          string         `json:"fetched_at,omitempty"`
}

type GatewayStatus struct {
	Reachable  bool             `json:"reachable"`
	HTTPStatus int              `json:"http_status"`
	InfoStatus int              `json:"info_status"`
	Mode       string           `json:"mode"`
	Inventory  InventorySummary `json:"inventory"`
	Note       string           `json:"note"`
}

type managedInventory struct {
	Cameras []managedInventoryCamera `json:"cameras"`
}

type managedInventoryCamera struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	ModelID string `json:"model_id"`
	Video   struct {
		State string `json:"state"`
	} `json:"video"`
	Streams []struct {
		ID       string `json:"id"`
		Protocol string `json:"protocol"`
		URL      string `json:"url"`
	} `json:"streams"`
}

func newGateway(c Config) *Gateway {
	return &Gateway{
		cfg:     c,
		cameras: make(map[string]gatewayCamera),
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					host, _, err := net.SplitHostPort(address)
					if err != nil || !isLoopback(host) {
						return nil, errors.New("gateway connection to non-loopback forbidden")
					}
					return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
				},
			},
		},
	}
}

func (g *Gateway) token() (string, error) {
	if g.cfg.TokenFile == "" {
		return "", errors.New("token_file is not configured")
	}
	b, err := os.ReadFile(g.cfg.TokenFile)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("token_file is empty")
	}
	return token, nil
}

func (g *Gateway) request(ctx context.Context, endpoint string, auth bool) ([]byte, int, error) {
	return g.do(ctx, http.MethodGet, endpoint, nil, auth)
}

func (g *Gateway) do(ctx context.Context, method, endpoint string, body []byte, auth bool) ([]byte, int, error) {
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.cfg.GatewayURL+endpoint, requestBody)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		tok, e := g.token()
		if e != nil {
			return nil, 0, e
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxGatewayResponse))
	return b, resp.StatusCode, err
}

func (g *Gateway) Status(ctx context.Context) GatewayStatus {
	g.mu.RLock()
	inv := g.inventorySummary
	inv.C201Cameras = append([]CameraOption(nil), inv.C201Cameras...)
	g.mu.RUnlock()
	status := GatewayStatus{Mode: "live", Inventory: inv, Note: "GET /v2/info is read-only; the camera session is prepared only by an explicit inventory or PTZ action"}
	_, code, err := g.request(ctx, "/healthz", false)
	status.HTTPStatus = code
	status.Reachable = err == nil && code == http.StatusOK
	_, infoCode, err2 := g.request(ctx, "/v2/info", true)
	if err2 == nil {
		status.InfoStatus = infoCode
	}
	return status
}

// RefreshInventory fetches the managed camera records and chooses only a C201
// RTSP endpoint from that same camera record. It never keeps an arbitrary
// stream URL from another camera.
func (g *Gateway) RefreshInventory(ctx context.Context) (InventorySummary, error) {
	b, code, err := g.request(ctx, "/v2/cameras", true)
	if err != nil {
		return InventorySummary{}, err
	}
	if code != http.StatusOK {
		return InventorySummary{}, fmt.Errorf("gateway inventory returned HTTP %d", code)
	}
	var document managedInventory
	if err := json.Unmarshal(b, &document); err != nil {
		return InventorySummary{}, fmt.Errorf("gateway inventory JSON: %w", err)
	}
	return g.applyInventory(document), nil
}

func (g *Gateway) applyInventory(document managedInventory) InventorySummary {
	now := time.Now()
	cameras := make(map[string]gatewayCamera)
	options := make([]CameraOption, 0)
	streams := 0
	for _, camera := range document.Cameras {
		modelName := camera.Model
		if modelName == "" {
			modelName = camera.ModelID
		}
		for _, stream := range camera.Streams {
			if (stream.Protocol == "" || stream.Protocol == "rtsp") && g.allowRTSP(stream.URL) {
				streams++
			}
		}
		if !strings.EqualFold(modelName, "xiaomi.camera.c201") || camera.ID == "" {
			continue
		}
		name := strings.TrimSpace(camera.Name)
		if name == "" {
			name = "Xiaomi C201"
		}
		item := gatewayCamera{
			option:     CameraOption{ID: camera.ID, Name: name},
			model:      modelName,
			videoState: strings.ToLower(camera.Video.State),
		}
		for _, stream := range camera.Streams {
			if (stream.Protocol != "" && stream.Protocol != "rtsp") || !g.allowRTSP(stream.URL) {
				continue
			}
			if item.rtspURL == "" || stream.ID == "live" || stream.ID == "main" {
				item.rtspURL = stream.URL
			}
			if stream.ID == "live" || stream.ID == "main" {
				break
			}
		}
		cameras[camera.ID] = item
		options = append(options, item.option)
	}

	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	g.cameras = cameras
	g.inventoryFetched = now
	g.selectedCameraID = ""
	for id := range cameras {
		if len(cameras) == 1 {
			g.selectedCameraID = id
		}
	}
	summary := InventorySummary{
		Loaded:             true,
		CameraCount:        len(document.Cameras),
		C201Count:          len(options),
		StreamURLsDetected: streams,
		C201Cameras:        options,
		SelectedCameraID:   g.selectedCameraID,
		SessionState:       "not_prepared",
		FetchedAt:          now.UTC().Format(time.RFC3339),
	}
	if g.selectedCameraID != "" {
		item := cameras[g.selectedCameraID]
		summary.SessionReady = item.videoState == "streaming" && item.rtspURL != ""
		summary.SessionState = item.videoState
	}
	g.inventorySummary = summary
	g.mu.Unlock()
	return summary
}

// SelectCamera pins a specific C201 from the current inventory. The identifier
// must have come from a managed inventory response.
func (g *Gateway) SelectCamera(cameraID string) error {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	item, ok := g.cameras[cameraID]
	if !ok {
		return errors.New("selected camera is not a C201 in the current inventory")
	}
	g.selectedCameraID = cameraID
	g.inventorySummary.SelectedCameraID = cameraID
	g.inventorySummary.SessionReady = item.videoState == "streaming" && item.rtspURL != ""
	g.inventorySummary.SessionState = item.videoState
	return nil
}

func (g *Gateway) SelectedCameraID() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.selectedCameraID
}

func (g *Gateway) inventorySnapshot() InventorySummary {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := g.inventorySummary
	result.C201Cameras = append([]CameraOption(nil), result.C201Cameras...)
	return result
}

// PrepareSelected waits for the selected camera's existing managed session to
// reach the gateway's streaming state. This establishes the CS2/MISS session;
// it does not run FFmpeg or inspect image stability.
func (g *Gateway) PrepareSelected(ctx context.Context) error {
	g.mu.RLock()
	id := g.selectedCameraID
	if id == "" && len(g.cameras) == 1 {
		for only := range g.cameras {
			id = only
		}
	}
	ready := id != "" && g.inventorySummary.SessionReady && g.inventorySummary.SelectedCameraID == id
	g.mu.RUnlock()
	if id == "" {
		return errors.New("select a C201 camera from the inventory")
	}
	if ready {
		return nil
	}
	return g.PrepareCamera(ctx, id)
}

func (g *Gateway) PrepareCamera(ctx context.Context, cameraID string) error {
	if cameraID == "" {
		return errors.New("select a C201 camera from the inventory")
	}
	g.mu.RLock()
	selected := g.selectedCameraID
	item, exists := g.cameras[cameraID]
	ready := exists && selected == cameraID && g.inventorySummary.SessionReady &&
		g.inventorySummary.SelectedCameraID == cameraID && item.rtspURL != ""
	g.mu.RUnlock()
	if selected != cameraID {
		return errors.New("camera selection changed before PTZ preparation; no request was sent")
	}
	if !exists {
		return errors.New("selected C201 is no longer available in the current inventory")
	}
	if ready {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			g.setSessionState(cameraID, "timeout", false)
			return errors.New("camera session is not ready yet; no PTZ request was sent")
		}
		b, code, err := g.request(ctx, "/v2/cameras", true)
		if err != nil {
			return fmt.Errorf("gateway camera session check failed: %w", err)
		}
		if code != http.StatusOK {
			return fmt.Errorf("gateway camera session check returned HTTP %d", code)
		}
		var document managedInventory
		if err := json.Unmarshal(b, &document); err != nil {
			return fmt.Errorf("gateway camera session response is invalid: %w", err)
		}
		item, found := findManagedC201(g, document, cameraID)
		if !found {
			return errors.New("selected C201 is missing from gateway inventory")
		}
		g.setCameraSession(cameraID, item)
		g.mu.RLock()
		selected = g.selectedCameraID
		current, currentExists := g.cameras[cameraID]
		ready = currentExists && g.inventorySummary.SessionReady && g.inventorySummary.SelectedCameraID == cameraID &&
			current.videoState == "streaming" && current.rtspURL != ""
		g.mu.RUnlock()
		if selected != cameraID {
			return errors.New("camera selection changed during PTZ preparation; no request was sent")
		}
		if !currentExists {
			return errors.New("selected C201 is no longer available in the current inventory")
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			g.setSessionState(cameraID, "timeout", false)
			return errors.New("camera session is not ready yet; no PTZ request was sent")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func findManagedC201(g *Gateway, document managedInventory, cameraID string) (gatewayCamera, bool) {
	for _, camera := range document.Cameras {
		if camera.ID != cameraID {
			continue
		}
		modelName := camera.Model
		if modelName == "" {
			modelName = camera.ModelID
		}
		if !strings.EqualFold(modelName, "xiaomi.camera.c201") {
			return gatewayCamera{}, false
		}
		item := gatewayCamera{option: CameraOption{ID: camera.ID, Name: camera.Name}, model: modelName, videoState: strings.ToLower(camera.Video.State)}
		for _, stream := range camera.Streams {
			if (stream.Protocol == "" || stream.Protocol == "rtsp") && g.allowRTSP(stream.URL) {
				item.rtspURL = stream.URL
				if stream.ID == "live" || stream.ID == "main" {
					break
				}
			}
		}
		return item, true
	}
	return gatewayCamera{}, false
}

func (g *Gateway) setCameraSession(cameraID string, item gatewayCamera) {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.selectedCameraID != cameraID {
		return
	}
	if _, exists := g.cameras[cameraID]; !exists {
		return
	}
	g.cameras[cameraID] = item
	g.inventorySummary.SessionReady = item.videoState == "streaming" && item.rtspURL != ""
	g.inventorySummary.SessionState = item.videoState
	g.inventorySummary.SelectedCameraID = cameraID
}

func (g *Gateway) setSessionState(cameraID, state string, ready bool) {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.selectedCameraID == cameraID {
		g.inventorySummary.SessionReady = ready
		g.inventorySummary.SessionState = state
	}
}

// Step sends one authenticated managed PTZ POST to the explicitly selected
// camera. The client has redirects disabled and deliberately performs no retry.
func (g *Gateway) Step(ctx context.Context, cameraID, direction string) (string, error) {
	if direction != "left" && direction != "right" && direction != "up" && direction != "down" {
		return "", errors.New("invalid PTZ direction")
	}
	// Keep selection and inventory mutations out until the authenticated write
	// completes. The camera checked below is therefore the camera receiving it.
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", errors.New("PTZ request expired before the gateway write; no request was sent")
	}
	g.mu.RLock()
	selected := g.selectedCameraID
	item, exists := g.cameras[cameraID]
	ready := g.inventorySummary.SessionReady && g.inventorySummary.SelectedCameraID == cameraID
	g.mu.RUnlock()
	if !exists {
		return "", errors.New("selected camera is not available in the current C201 inventory")
	}
	if selected != cameraID {
		return "", errors.New("camera selection changed before PTZ step; no request was sent")
	}
	if !ready || item.rtspURL == "" {
		return "", errors.New("camera session is not connected")
	}
	body, err := json.Marshal(map[string]string{"direction": direction})
	if err != nil {
		return "", errors.New("could not encode PTZ request")
	}
	endpoint := "/v2/cameras/" + url.PathEscape(cameraID) + "/ptz/step"
	response, code, err := g.do(ctx, http.MethodPost, endpoint, body, true)
	if err != nil {
		return "", fmt.Errorf("gateway PTZ request failed and was not retried: %w", err)
	}
	if code != http.StatusOK {
		return "", gatewayAPIError(code, response)
	}
	var result struct {
		WriteStatus string `json:"write_status"`
	}
	if err := json.Unmarshal(response, &result); err != nil || result.WriteStatus != "sent" {
		return "", errors.New("gateway returned an invalid PTZ result")
	}
	return result.WriteStatus, nil
}

func gatewayAPIError(status int, body []byte) error {
	var response struct {
		Error string `json:"error"`
	}
	code := ""
	if json.Unmarshal(body, &response) == nil && len(response.Error) > 0 && len(response.Error) <= 64 {
		valid := true
		for _, ch := range response.Error {
			if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '_' {
				valid = false
				break
			}
		}
		if valid {
			code = response.Error
		}
	}
	if code != "" {
		return fmt.Errorf("gateway PTZ API returned HTTP %d (%s)", status, code)
	}
	return fmt.Errorf("gateway PTZ API returned HTTP %d", status)
}

func (g *Gateway) allowRTSP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "rtsp" || u.Hostname() == "" || u.User != nil {
		return false
	}
	host := u.Hostname()
	if isLoopback(host) {
		return true
	}
	for _, allowed := range g.cfg.AllowedRTSPHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}

func (g *Gateway) RTSPURL() (string, error) {
	g.mu.RLock()
	cameraID := g.selectedCameraID
	item, exists := g.cameras[cameraID]
	ready := cameraID != "" && exists && g.inventorySummary.SelectedCameraID == cameraID &&
		g.inventorySummary.SessionReady && item.videoState == "streaming" && item.rtspURL != ""
	raw := item.rtspURL
	g.mu.RUnlock()
	if !ready {
		return "", errors.New("selected C201 has no ready managed RTSP stream")
	}
	if !g.allowRTSP(raw) {
		return "", errors.New("selected C201 RTSP URL host is not allowed or URL includes credentials")
	}
	return raw, nil
}
