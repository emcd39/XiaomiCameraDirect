// Package streamrtsp republishes raw Annex-B H.264 and H.265 access units as a local,
// TCP-interleaved RTSP stream for Home Assistant.
package streamrtsp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
)

const (
	defaultPayloadType = 96
	clockRate          = 90000
	maxReplayGOPBytes  = 4 << 20
	rtspWriteQueueSize = 2048
	// Replay is queued before the RTSP writer is started. Leave space for
	// live packets arriving as the reader is added to the shared stream.
	maxReplayGOPPackets = rtspWriteQueueSize / 2
)

// Relay serves capability URLs and can host multiple independently managed
// camera paths. Only RTSP-over-TCP is enabled, so no extra UDP ports are needed.
type Relay struct {
	server *gortsplib.Server
	mu     sync.RWMutex
	paths  map[string]*Path
}

// Path is an unguessable RTSP capability URL backed by one live camera.
type Path struct {
	id      string
	relay   *Relay
	server  *gortsplib.Server
	ready   chan struct{}
	readyMu sync.Once

	mu           sync.RWMutex
	stream       *gortsplib.ServerStream
	media        *description.Media
	codec        string
	sourceFormat model.VideoFormat
	sourceSPS    []byte
	enc          interface {
		Encode(au [][]byte) ([]*rtp.Packet, error)
	}
	vps []byte
	sps []byte
	pps []byte

	replayGOP       []*rtp.Packet
	replayGOPBytes  int
	replayGOPUsable bool
}

// New starts an RTSP TCP listener.
func New(address string) (*Relay, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("RTSP listen address is required")
	}
	r := &Relay{paths: make(map[string]*Path)}
	r.server = &gortsplib.Server{RTSPAddress: address, Handler: r, WriteQueueSize: rtspWriteQueueSize}
	if err := r.server.Start(); err != nil {
		return nil, fmt.Errorf("start RTSP listener: %w", err)
	}
	return r, nil
}

// NewPath allocates a random path token. The caller must close the path when
// the Xiaomi session is retired.
func (r *Relay) NewPath(codec string) (*Path, error) {
	if codec != "H264" && codec != "H265" {
		return nil, errors.New("unsupported RTSP video codec")
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	id := "s_" + hex.EncodeToString(token[:])
	p := &Path{id: id, relay: r, server: r.server, ready: make(chan struct{}), codec: codec}
	r.mu.Lock()
	r.paths[id] = p
	r.mu.Unlock()
	return p, nil
}

// URL returns the credential-free capability URL shown only to an
// authenticated Gateway API client.
func (p *Path) URL(host string, port int) (string, error) {
	if strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/?#@") || port < 1 || port > 65535 {
		return "", errors.New("invalid RTSP advertised address")
	}
	return fmt.Sprintf("rtsp://%s/%s", net.JoinHostPort(host, fmt.Sprint(port)), p.id), nil
}

// Ready reports whether the upstream has supplied enough video parameters to
// serve SDP and a random-access frame to start playback.
func (p *Path) Ready() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stream != nil
}

// ReadySignal closes when the first valid stream description is ready.
func (p *Path) ReadySignal() <-chan struct{} { return p.ready }

// SourceFormat snapshots the format of the currently published stream.
func (p *Path) SourceFormat() *model.VideoFormat {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.stream == nil {
		return nil
	}
	result := p.sourceFormat
	return &result
}

// WriteAccessUnit accepts one Annex-B access unit in the path's codec. The stream is
// created only after parameter sets and a key frame have arrived.
func (p *Path) WriteAccessUnit(data []byte, ptsMS int64, keyframe bool) error {
	nalus := splitAnnexB(data)
	if len(nalus) == 0 {
		return errors.New("empty video access unit")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, nalu := range nalus {
		if p.codec == "H264" {
			switch h264.NALUType(nalu[0] & 0x1f) {
			case h264.NALUTypeSPS:
				var sps h264.SPS
				if err := sps.Unmarshal(nalu); err != nil {
					return fmt.Errorf("invalid H.264 SPS: %w", err)
				}
				p.sps = append(p.sps[:0], nalu...)
			case h264.NALUTypePPS:
				if len(nalu) < 2 {
					return errors.New("invalid H.264 PPS")
				}
				p.pps = append(p.pps[:0], nalu...)
			}
			continue
		}
		if len(nalu) < 2 {
			continue
		}
		switch (nalu[0] >> 1) & 0x3f {
		case 32:
			p.vps = append(p.vps[:0], nalu...)
		case 33:
			p.sps = append(p.sps[:0], nalu...)
		case 34:
			p.pps = append(p.pps[:0], nalu...)
		}
	}

	if p.stream == nil {
		if !keyframe || len(p.sps) == 0 || len(p.pps) == 0 || (p.codec == "H265" && len(p.vps) == 0) {
			return nil
		}
		if err := p.initializeStreamLocked(); err != nil {
			return err
		}
	}

	packets, err := p.enc.Encode(nalus)
	if err != nil {
		return fmt.Errorf("packetize %s access unit: %w", p.codec, err)
	}
	stamp := uint32(ptsMS * clockRate / 1000)
	if keyframe {
		p.replayGOP = p.replayGOP[:0]
		p.replayGOPBytes = 0
		p.replayGOPUsable = true
	}
	for i, packet := range packets {
		packet.Timestamp = stamp
		packet.Marker = i == len(packets)-1
		if err := p.stream.WritePacketRTP(p.media, packet); err != nil {
			p.clearReplayGOPLocked()
			return fmt.Errorf("write RTSP packet: %w", err)
		}
		p.cacheReplayPacketLocked(packet)
	}
	if keyframe && !bytes.Equal(p.sourceSPS, p.sps) {
		p.updateSourceFormatLocked()
	}
	return nil
}

func (p *Path) updateSourceFormatLocked() {
	p.sourceSPS = append(p.sourceSPS[:0], p.sps...)
	p.sourceFormat = model.VideoFormat{Codec: p.codec}
	var sps interface {
		Unmarshal([]byte) error
		Width() int
		Height() int
	} = &h265.SPS{}
	if p.codec == "H264" {
		sps = &h264.SPS{}
	}
	if sps.Unmarshal(p.sourceSPS) == nil {
		width, height := sps.Width(), sps.Height()
		if width > 0 && height > 0 {
			p.sourceFormat.Width, p.sourceFormat.Height = width, height
		}
	}
}

func (p *Path) initializeStreamLocked() error {
	var forma format.Format
	var encoder interface {
		Encode(au [][]byte) ([]*rtp.Packet, error)
	}
	var err error
	if p.codec == "H264" {
		video := &format.H264{
			PayloadTyp:        defaultPayloadType,
			SPS:               append([]byte(nil), p.sps...),
			PPS:               append([]byte(nil), p.pps...),
			PacketizationMode: 1,
		}
		forma = video
		encoder, err = video.CreateEncoder()
	} else {
		video := &format.H265{
			PayloadTyp: defaultPayloadType,
			VPS:        append([]byte(nil), p.vps...),
			SPS:        append([]byte(nil), p.sps...),
			PPS:        append([]byte(nil), p.pps...),
		}
		forma = video
		encoder, err = video.CreateEncoder()
	}
	if err != nil {
		return fmt.Errorf("create %s RTP encoder: %w", p.codec, err)
	}
	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{forma},
	}
	stream := &gortsplib.ServerStream{
		Server: p.server,
		Desc:   &description.Session{Medias: []*description.Media{media}},
	}
	if err := stream.Initialize(); err != nil {
		return fmt.Errorf("initialize RTSP stream: %w", err)
	}
	p.media = media
	p.enc = encoder
	p.stream = stream
	p.sourceFormat = model.VideoFormat{Codec: p.codec}
	p.readyMu.Do(func() { close(p.ready) })
	return nil
}

// Close retires the RTSP media stream but keeps the capability path available
// so a reconnect can publish a fresh stream at the same URL.
func (p *Path) CloseStream() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stream != nil {
		p.stream.Close()
		p.stream = nil
		p.media = nil
		p.enc = nil
	}
	p.vps, p.sps, p.pps = nil, nil, nil
	p.sourceSPS = nil
	p.clearReplayGOPLocked()
}

// Close removes a path capability and its stream.
func (p *Path) Close() {
	p.CloseStream()
	if p.relay != nil {
		p.relay.mu.Lock()
		delete(p.relay.paths, p.id)
		p.relay.mu.Unlock()
	}
}

// Close stops the RTSP listener and closes every media stream.
func (r *Relay) Close() {
	r.mu.Lock()
	paths := make([]*Path, 0, len(r.paths))
	for _, p := range r.paths {
		paths = append(paths, p)
	}
	r.paths = make(map[string]*Path)
	r.mu.Unlock()
	for _, p := range paths {
		p.CloseStream()
	}
	r.server.Close()
}

func (r *Relay) path(id string) *Path {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.paths[strings.TrimPrefix(id, "/")]
}

func (r *Relay) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	p := r.path(ctx.Path)
	if p == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.stream == nil {
		return &base.Response{StatusCode: base.StatusServiceUnavailable}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
}

func (r *Relay) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	p := r.path(ctx.Path)
	if p == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.stream == nil {
		return &base.Response{StatusCode: base.StatusServiceUnavailable}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
}

func (r *Relay) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	p := r.path(ctx.Path)
	if p == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil
	}

	// gortsplib calls OnPlay before it starts forwarding the shared stream to
	// this reader. Replay the most recent random-access point and its dependent
	// frames directly to the new session, so a client joining mid-GOP doesn't
	// begin decoding with reference pictures it never received.
	if ctx.Session.State() != gortsplib.ServerSessionStatePlay {
		ready, err := p.replayLatestGOP(ctx.Session.SetuppedStream(), func(media *description.Media, packet *rtp.Packet) error {
			return ctx.Session.WritePacketRTP(media, packet)
		})
		if err != nil {
			return &base.Response{StatusCode: base.StatusServiceUnavailable}, fmt.Errorf("replay GOP to RTSP client: %w", err)
		}
		if !ready {
			return &base.Response{StatusCode: base.StatusServiceUnavailable}, nil
		}
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (p *Path) replayLatestGOP(stream *gortsplib.ServerStream, write func(*description.Media, *rtp.Packet) error) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.stream == nil || stream != p.stream {
		return false, nil
	}
	for _, packet := range p.replayGOP {
		if err := write(p.media, packet.Clone()); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (p *Path) cacheReplayPacketLocked(packet *rtp.Packet) {
	if !p.replayGOPUsable {
		return
	}
	size := packet.MarshalSize()
	if len(p.replayGOP) >= maxReplayGOPPackets || p.replayGOPBytes+size > maxReplayGOPBytes {
		p.clearReplayGOPLocked()
		return
	}
	p.replayGOP = append(p.replayGOP, packet.Clone())
	p.replayGOPBytes += size
}

func (p *Path) clearReplayGOPLocked() {
	p.replayGOP = nil
	p.replayGOPBytes = 0
	p.replayGOPUsable = false
}

func splitAnnexB(data []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+3 < len(data); i++ {
		if data[i] != 0 || data[i+1] != 0 {
			continue
		}
		prefix := 0
		if data[i+2] == 1 {
			prefix = 3
		} else if i+4 <= len(data) && data[i+2] == 0 && data[i+3] == 1 {
			prefix = 4
		}
		if prefix == 0 {
			continue
		}
		if start >= 0 && i > start {
			out = append(out, data[start:i])
		}
		start = i + prefix
		// Skip the remainder of a four-byte start code.
		if prefix == 4 {
			i++
		}
	}
	if start >= 0 && start < len(data) {
		out = append(out, data[start:])
	}
	return out
}
