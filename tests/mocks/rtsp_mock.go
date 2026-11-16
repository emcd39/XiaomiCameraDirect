package main

import (
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
)

type serverHandler struct {
	stream *gortsplib.ServerStream
}

func (h *serverHandler) OnDescribe(_ *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	fmt.Println("MOCK_RTSP action=describe")
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *serverHandler) OnSetup(_ *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	fmt.Println("MOCK_RTSP action=setup")
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *serverHandler) OnPlay(_ *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	fmt.Println("MOCK_RTSP action=play")
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func main() {
	file := flag.String("file", "/tmp/mock-c201.h264", "Annex-B H.264 test frame")
	address := flag.String("address", ":8554", "RTSP listen address")
	flag.Parse()

	data, err := os.ReadFile(*file)
	if err != nil {
		fatal(err)
	}
	nalus := splitAnnexB(data)
	var sps, pps []byte
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		switch nalu[0] & 0x1f {
		case 7:
			sps = append([]byte(nil), nalu...)
		case 8:
			pps = append([]byte(nil), nalu...)
		}
	}
	if len(sps) == 0 || len(pps) == 0 || len(nalus) == 0 {
		fatal(fmt.Errorf("test H.264 sample is missing parameter sets"))
	}
	forma := &format.H264{PayloadTyp: 96, SPS: sps, PPS: pps, PacketizationMode: 1}
	encoder, err := forma.CreateEncoder()
	if err != nil {
		fatal(err)
	}
	media := &description.Media{Type: description.MediaTypeVideo, Formats: []format.Format{forma}}
	server := &gortsplib.Server{RTSPAddress: *address}
	stream := &gortsplib.ServerStream{
		Server: server,
		Desc:   &description.Session{Medias: []*description.Media{media}},
	}
	handler := &serverHandler{stream: stream}
	server.Handler = handler
	if err := server.Start(); err != nil {
		fatal(err)
	}
	if err := stream.Initialize(); err != nil {
		fatal(err)
	}
	defer func() {
		stream.Close()
		server.Close()
	}()
	fmt.Println("MOCK_RTSP ready=true")

	var writeMu sync.Mutex
	start := time.Now()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var packetCount int
	for now := range ticker.C {
		packets, err := encoder.Encode(nalus)
		if err != nil {
			fatal(err)
		}
		stamp := uint32(now.Sub(start).Seconds() * 90000)
		for index, packet := range packets {
			packet.Timestamp = stamp
			packet.Marker = index == len(packets)-1
		}
		writeMu.Lock()
		for _, packet := range packets {
			if err := stream.WritePacketRTP(media, packet); err != nil {
				writeMu.Unlock()
				fatal(err)
			}
			packetCount++
		}
		writeMu.Unlock()
		if packetCount == len(packets) {
			fmt.Printf("MOCK_RTSP payload nalus=%d packets=%d bytes=%d stats=%+v\n", len(nalus), len(packets), len(data), stream.Stats())
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mock RTSP error:", err)
	os.Exit(1)
}

func splitAnnexB(data []byte) [][]byte {
	var units [][]byte
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
			units = append(units, data[start:i])
		}
		start = i + prefix
		if prefix == 4 {
			i++
		}
	}
	if start >= 0 && start < len(data) {
		unit := data[start:]
		for len(unit) > 0 && unit[len(unit)-1] == 0 {
			unit = unit[:len(unit)-1]
		}
		if len(unit) > 0 {
			units = append(units, unit)
		}
	}
	return units
}
