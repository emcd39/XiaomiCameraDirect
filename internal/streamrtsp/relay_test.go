package streamrtsp

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
)

var (
	testH265VPS = []byte{
		0x40, 0x01, 0x0c, 0x01, 0xff, 0xff, 0x01, 0x60,
		0x00, 0x00, 0x03, 0x00, 0x90, 0x00, 0x00, 0x03,
		0x00, 0x00, 0x03, 0x00, 0x78, 0x99, 0x98, 0x09,
	}
	testH265SPS = []byte{
		0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03,
		0x00, 0x90, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03,
		0x00, 0x78, 0xa0, 0x03, 0xc0, 0x80, 0x10, 0xe5,
		0x96, 0x66, 0x69, 0x24, 0xca, 0xe0, 0x10, 0x00,
		0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03, 0x01,
		0xe0, 0x80,
	}
	testH265PPS = []byte{0x44, 0x01, 0xc1, 0x72, 0xb4, 0x62, 0x40}
)

func testH265KeyFrame(marker byte, includeParameters bool) []byte {
	var data []byte
	if includeParameters {
		for _, nalu := range [][]byte{testH265VPS, testH265SPS, testH265PPS} {
			data = append(data, 0, 0, 1)
			data = append(data, nalu...)
		}
	}
	data = append(data, 0, 0, 1, 0x26, 0x01, marker)
	return data
}

func TestSplitAnnexBHandlesThreeAndFourByteStartCodes(t *testing.T) {
	input := []byte{0, 0, 0, 1, 0x40, 1, 0xaa, 0, 0, 1, 0x42, 1, 0xbb}
	got := splitAnnexB(input)
	want := [][]byte{{0x40, 1, 0xaa}, {0x42, 1, 0xbb}}
	if len(got) != len(want) {
		t.Fatalf("NALU count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("NALU %d = %x, want %x", i, got[i], want[i])
		}
	}
}

func TestPathBecomesReadyOnlyAfterParametersAndKeyFrame(t *testing.T) {
	relay, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	path, err := relay.NewPath("H265")
	if err != nil {
		t.Fatal(err)
	}
	defer path.Close()

	if err := path.WriteAccessUnit([]byte{0, 0, 1, 0x26, 1, 0xaa}, 1, false); err != nil {
		t.Fatal(err)
	}
	if path.Ready() {
		t.Fatal("path became ready without parameter sets")
	}
	accessUnit := testH265KeyFrame(0xdd, true)
	if err := path.WriteAccessUnit(accessUnit, 2, true); err != nil {
		t.Fatal(err)
	}
	if !path.Ready() {
		t.Fatal("path did not become ready after H.265 parameters and a key frame")
	}
	if _, err := path.URL("gateway", 8554); err != nil {
		t.Fatalf("build RTSP URL: %v", err)
	}
}

func TestLateClientReplayStartsAtLatestKeyFrame(t *testing.T) {
	relay, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	path, err := relay.NewPath("H265")
	if err != nil {
		t.Fatal(err)
	}
	defer path.Close()

	firstKeyFrame := testH265KeyFrame(0xa3, true)
	if err := path.WriteAccessUnit(firstKeyFrame, 100, true); err != nil {
		t.Fatal(err)
	}
	if err := path.WriteAccessUnit([]byte{0, 0, 1, 0x02, 1, 0xb0}, 200, false); err != nil {
		t.Fatal(err)
	}
	// A later IDR replaces the replay window. A client joining after this
	// point must not receive the earlier GOP's packets.
	if err := path.WriteAccessUnit(testH265KeyFrame(0xc0, false), 300, true); err != nil {
		t.Fatal(err)
	}
	if err := path.WriteAccessUnit([]byte{0, 0, 1, 0x02, 1, 0xc1}, 400, false); err != nil {
		t.Fatal(err)
	}

	var replayed []*rtp.Packet
	ready, err := path.replayLatestGOP(path.stream, func(_ *description.Media, packet *rtp.Packet) error {
		replayed = append(replayed, packet)
		return nil
	})
	if err != nil {
		t.Fatalf("replay latest GOP: %v", err)
	}
	if !ready {
		t.Fatal("path was not ready for replay")
	}
	if len(replayed) != 2 {
		t.Fatalf("replayed %d RTP packets, want the latest IDR and one dependent frame", len(replayed))
	}
	if got := (replayed[0].Payload[0] >> 1) & 0x3f; got != 19 {
		t.Fatalf("first replayed H.265 NALU type = %d, want IDR type 19", got)
	}
	if got := (replayed[1].Payload[0] >> 1) & 0x3f; got != 1 {
		t.Fatalf("second replayed H.265 NALU type = %d, want dependent frame type 1", got)
	}
	if bytes.Contains(replayed[0].Payload, []byte{0xa3}) || bytes.Contains(replayed[1].Payload, []byte{0xb0}) {
		t.Fatal("replay included packets from before the latest IDR")
	}
}

func TestReplayRejectsStreamFromPreviousCameraSession(t *testing.T) {
	relay, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	path, err := relay.NewPath("H265")
	if err != nil {
		t.Fatal(err)
	}
	defer path.Close()

	if err := path.WriteAccessUnit(testH265KeyFrame(0xa3, true), 100, true); err != nil {
		t.Fatal(err)
	}
	oldStream := path.stream
	path.CloseStream()
	if err := path.WriteAccessUnit(testH265KeyFrame(0xc0, true), 200, true); err != nil {
		t.Fatal(err)
	}

	called := false
	ready, err := path.replayLatestGOP(oldStream, func(_ *description.Media, _ *rtp.Packet) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("replay previous session: %v", err)
	}
	if ready || called {
		t.Fatal("replay accepted a stream from a previous camera session")
	}
}

func TestReplayGOPBufferIsBounded(t *testing.T) {
	path := &Path{replayGOPUsable: true}
	path.mu.Lock()
	defer path.mu.Unlock()
	path.cacheReplayPacketLocked(&rtp.Packet{Payload: make([]byte, maxReplayGOPBytes+1)})
	if path.replayGOPUsable || len(path.replayGOP) != 0 || path.replayGOPBytes != 0 {
		t.Fatal("oversized GOP was retained in the replay buffer")
	}
	path.cacheReplayPacketLocked(&rtp.Packet{Payload: []byte{0x26, 1}})
	if len(path.replayGOP) != 0 {
		t.Fatal("dependent packets were retained after an oversized GOP")
	}

	path.replayGOPUsable = true
	packet := &rtp.Packet{Payload: []byte{0x26, 1}}
	for range maxReplayGOPPackets {
		path.cacheReplayPacketLocked(packet)
	}
	if len(path.replayGOP) != maxReplayGOPPackets {
		t.Fatalf("replay retained %d packets within queue budget", len(path.replayGOP))
	}
	path.cacheReplayPacketLocked(packet)
	if path.replayGOPUsable || len(path.replayGOP) != 0 {
		t.Fatal("a GOP exceeding the writer queue budget was retained")
	}
}

func TestRTSPLateClientReceivesReplayBeforeLivePackets(t *testing.T) {
	t.Run("H265", func(t *testing.T) {
		testRTSPLateClient(t, "H265", testH265KeyFrame(0xa3, true),
			testH265KeyFrame(0xc0, false), []byte{0, 0, 1, 0x02, 1, 0xb0}, 19)
	})
	t.Run("H264", func(t *testing.T) {
		// libx264 baseline, two 16x16 black frames, with encoder SEI removed.
		sps := []byte{0x67, 0x42, 0xc0, 0x0a, 0xda, 0x7b, 0x01, 0x10,
			0x00, 0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03, 0x00, 0x40, 0xf1, 0x22, 0x6a}
		parameters := append([]byte{0, 0, 1}, sps...)
		parameters = append(parameters, 0, 0, 1, 0x68, 0xce, 0x0f, 0xc8)
		idr := []byte{0, 0, 1, 0x65, 0x88, 0x84, 0x3a, 0x26, 0x28, 0x00, 0x09, 0x02, 0xe0}
		dependent := []byte{0, 0, 1, 0x41, 0x9a, 0x20, 0x12, 0xa5}
		testRTSPLateClient(t, "H264", append(parameters, idr...), idr, dependent, 5)
	})
}

func testRTSPLateClient(t *testing.T, codec string, firstKeyFrame, secondKeyFrame, dependent []byte, keyType uint8) {
	t.Helper()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	port := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}

	relay, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	path, err := relay.NewPath(codec)
	if err != nil {
		t.Fatal(err)
	}
	defer path.Close()

	if err := path.WriteAccessUnit(secondKeyFrame, 0, true); err != nil {
		t.Fatal(err)
	}
	if path.Ready() {
		t.Fatalf("%s path became ready before receiving parameter sets", codec)
	}
	if path.SourceFormat() != nil {
		t.Fatalf("%s advertised a format before becoming ready", codec)
	}
	if err := path.WriteAccessUnit(firstKeyFrame, 100, true); err != nil {
		t.Fatal(err)
	}
	source := path.SourceFormat()
	width, height := 1920, 1080
	if codec == "H264" {
		width, height = 16, 16
	}
	if source == nil || source.Codec != codec || source.Width != width || source.Height != height {
		t.Fatalf("source format = %+v, want %s %dx%d", source, codec, width, height)
	}
	if err := path.WriteAccessUnit(dependent, 200, false); err != nil {
		t.Fatal(err)
	}
	// The client connects after the live source has advanced beyond this GOP.
	if err := path.WriteAccessUnit(secondKeyFrame, 300, true); err != nil {
		t.Fatal(err)
	}
	if err := path.WriteAccessUnit(dependent, 400, false); err != nil {
		t.Fatal(err)
	}

	streamURL, err := path.URL("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	u, err := base.ParseURL(streamURL)
	if err != nil {
		t.Fatal(err)
	}
	transport := gortsplib.TransportTCP
	client := &gortsplib.Client{Transport: &transport}
	client.Scheme = u.Scheme
	client.Host = u.Host
	if err := client.Start2(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	desc, _, err := client.Describe(u)
	if err != nil {
		t.Fatal(err)
	}
	var media *description.Media
	var video format.Format
	naluType := func(header byte) byte { return (header >> 1) & 0x3f }
	if codec == "H264" {
		var h264 *format.H264
		media = desc.FindFormat(&h264)
		if h264 == nil || len(h264.SPS) == 0 || len(h264.PPS) == 0 || h264.PacketizationMode != 1 {
			t.Fatalf("missing H.264 configuration in RTSP description: %+v", h264)
		}
		video = h264
		naluType = func(header byte) byte { return header & 0x1f }
	} else {
		var h265 *format.H265
		media = desc.FindFormat(&h265)
		video = h265
	}
	if media == nil {
		t.Fatalf("%s media was not present in RTSP description", codec)
	}
	if _, err := client.Setup(desc.BaseURL, media, 0, 0); err != nil {
		t.Fatal(err)
	}
	types := make(chan uint8, 8)
	client.OnPacketRTP(media, video, func(packet *rtp.Packet) {
		if len(packet.Payload) > 0 {
			types <- naluType(packet.Payload[0])
		}
	})
	if _, err := client.Play(nil); err != nil {
		t.Fatal(err)
	}

	waitType := func(want uint8) {
		t.Helper()
		select {
		case got := <-types:
			if got != want {
				t.Fatalf("received %s NALU type %d, want %d", codec, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s NALU type %d", codec, want)
		}
	}
	waitType(keyType) // most recent IDR replayed by OnPlay
	waitType(1)       // dependent frame replayed after its IDR

	if err := path.WriteAccessUnit(dependent, 500, false); err != nil {
		t.Fatal(err)
	}
	waitType(1) // live delivery continues after the replay

	client.Close()
	if codec == "H265" {
		// 1920x800 SPS fixture from mediacommon's H.265 SPS tests.
		newParameters := []byte{0, 0, 1,
			0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03,
			0x00, 0x90, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03,
			0x00, 0x78, 0xa0, 0x03, 0xc0, 0x80, 0x32, 0x16,
			0x59, 0x59, 0xa4, 0x93, 0x2b, 0xc0, 0x5a, 0x80,
			0x80, 0x80, 0x82, 0x00, 0x00, 0x07, 0xd2, 0x00,
			0x00, 0xbb, 0x80, 0x10,
		}
		if err := path.WriteAccessUnit(newParameters, 550, false); err != nil {
			t.Fatal(err)
		}
		if current := path.SourceFormat(); current.Height != 1080 {
			t.Fatalf("source format changed before keyframe: %+v", current)
		}
		if err := path.WriteAccessUnit(secondKeyFrame, 600, true); err != nil {
			t.Fatal(err)
		}
		if current := path.SourceFormat(); current.Width != 1920 || current.Height != 800 {
			t.Fatalf("source format after switch = %+v, want 1920x800", current)
		}
	}
	path.CloseStream()
	if path.SourceFormat() != nil {
		t.Fatal("retired stream still advertises a format")
	}
}
