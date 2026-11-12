package stream

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/protocol/miss"
)

func TestMotorOperationMapsAllDirections(t *testing.T) {
	for _, testCase := range []struct {
		direction string
		want      int
	}{
		{direction: "left", want: miss.MotorPanPlus},
		{direction: "right", want: miss.MotorPanMinus},
		{direction: "up", want: miss.MotorTiltMinus},
		{direction: "down", want: miss.MotorTiltPlus},
	} {
		got, err := motorOperation(testCase.direction)
		if err != nil || got != testCase.want {
			t.Fatalf("motorOperation(%q) = %d, %v; want %d", testCase.direction, got, err, testCase.want)
		}
	}
	if _, err := motorOperation("diagonal"); err == nil {
		t.Fatal("unknown motor direction was accepted")
	}
}

func TestObservePTZStepReportsWriteAndSanitizedReply(t *testing.T) {
	start := time.Date(2026, time.November, 7, 20, 0, 0, 0, time.UTC)
	current := start
	var pending []reply
	var sendCalls int
	send := func() error {
		sendCalls++
		return nil
	}
	drain := func() []reply {
		out := pending
		pending = nil
		return out
	}
	wait := func(duration time.Duration) {
		current = current.Add(duration)
		if current.Sub(start) >= 50*time.Millisecond && pending == nil {
			pending = []reply{{
				cmd:        ptzReplyCommandID,
				body:       []byte(`{"ret":0,"angle":12.5,"did":"private-did","token":"private-token"}`),
				receivedAt: current,
			}}
		}
	}
	result := observePTZStep("C201", "cs2+tcp", 1, send, drain, func() time.Time { return current }, wait)

	if sendCalls != 1 || result.writeStatus != "write_ok" {
		t.Fatalf("expected one successful write, calls=%d status=%q", sendCalls, result.writeStatus)
	}
	if result.replyStatus != "reply_received" || result.replyCommandID != ptzReplyCommandID {
		t.Fatalf("expected decoded 0x113, status=%q cmd=%#x", result.replyStatus, result.replyCommandID)
	}
	if got := result.replyLatency; got != 50*time.Millisecond {
		t.Fatalf("reply latency = %s, want 50ms", got)
	}
	line := result.SanitizedLine()
	for _, want := range []string{"cmd_sent_id=0x112", `payload={"operation":1}`, "write_status=write_ok", "reply_status=reply_received", "reply_cmd_id=0x113", `reply_fields={"angle":12.5,"ret":0}`, "reply_latency_ms=50", "motion=not_measured"} {
		if !strings.Contains(line, want) {
			t.Errorf("sanitized line missing %q: %s", want, line)
		}
	}
	for _, secret := range []string{"private-did", "private-token", "session", "https://"} {
		if strings.Contains(line, secret) {
			t.Errorf("sanitized line leaked %q: %s", secret, line)
		}
	}
}

func TestObservePTZStepReportsNoReplyAtTwoSecondDeadline(t *testing.T) {
	start := time.Date(2026, time.November, 7, 20, 0, 0, 0, time.UTC)
	current := start
	result := observePTZStep("C201", "cs2+tcp", 1,
		func() error { return nil },
		func() []reply { return nil },
		func() time.Time { return current },
		func(duration time.Duration) { current = current.Add(duration) },
	)
	if result.writeStatus != "write_ok" || result.replyStatus != "no_reply" {
		t.Fatalf("statuses = %s/%s, want write_ok/no_reply", result.writeStatus, result.replyStatus)
	}
	if elapsed := current.Sub(start); elapsed != ptzReplyTimeout {
		t.Fatalf("waited %s, want %s", elapsed, ptzReplyTimeout)
	}
	if !strings.Contains(result.SanitizedLine(), "reply_timeout_ms=2000") {
		t.Fatalf("missing two-second no-reply deadline: %s", result.SanitizedLine())
	}
}

func TestObservePTZStepRejectsLateReplyAfterDelayedWake(t *testing.T) {
	start := time.Date(2026, time.November, 7, 20, 0, 0, 0, time.UTC)
	current := start
	deadline := start.Add(ptzReplyTimeout)
	var pending []reply
	var delayed bool
	result := observePTZStep("C201", "cs2+tcp", 1,
		func() error { return nil },
		func() []reply {
			out := pending
			pending = nil
			return out
		},
		func() time.Time { return current },
		func(duration time.Duration) {
			if !delayed && current.Add(duration).Equal(deadline) {
				// The response arrives after the deadline while the polling goroutine
				// is asleep, then the scheduler wakes it 100 ms late.
				pending = []reply{{
					cmd:        ptzReplyCommandID,
					body:       []byte(`{"ret":0,"angle":12}`),
					receivedAt: deadline.Add(time.Millisecond),
				}}
				current = deadline.Add(100 * time.Millisecond)
				delayed = true
				return
			}
			current = current.Add(duration)
		},
	)

	if !delayed {
		t.Fatal("test did not simulate an overdue wake")
	}
	if result.writeStatus != "write_ok" || result.replyStatus != "no_reply" {
		t.Fatalf("late reply was accepted: %s", result.SanitizedLine())
	}
	if result.lateReplyCount != 1 || !result.replyReceivedAt.IsZero() || result.replyFields != nil {
		t.Fatalf("late reply evidence was not kept separate: %s", result.SanitizedLine())
	}
	line := result.SanitizedLine()
	for _, want := range []string{"reply_status=no_reply", "reply_cmd_id=0x113", "reply_fields={}", "reply_received_at=none", "reply_latency_ms=none", "reply_timeout_ms=2000", "late_reply_count=1", "motion=not_measured"} {
		if !strings.Contains(line, want) {
			t.Errorf("sanitized late-reply line missing %q: %s", want, line)
		}
	}
}

func TestObservePTZStepSeparatesWriteErrorFromNoReply(t *testing.T) {
	start := time.Now()
	current := start
	waitCalls := 0
	result := observePTZStep("C201", "cs2+tcp", 1,
		func() error { return errors.New("private transport detail") },
		func() []reply { return nil },
		func() time.Time { return current },
		func(time.Duration) { waitCalls++ },
	)
	line := result.SanitizedLine()
	if result.writeStatus != "write_error" || result.replyStatus != "not_waited" || waitCalls != 0 {
		t.Fatalf("write failure collapsed into another state: %s", line)
	}
	if strings.Contains(line, "private transport detail") {
		t.Fatalf("sanitized line leaked error text: %s", line)
	}
}

func TestPTZDiagnosticLineRejectsIdentifiersAsLabels(t *testing.T) {
	result := PTZDiagnosticResult{
		cameraLabel: "123456789012",
		transport:   "192.168.3.119",
		operation:   1,
		writeStatus: "write_ok",
		replyStatus: "no_reply",
	}
	line := result.SanitizedLine()
	if strings.Contains(line, "123456789012") || strings.Contains(line, "192.168.3.119") {
		t.Fatalf("sanitized line leaked an identifier or address: %s", line)
	}
	if !strings.Contains(line, "camera=unknown transport=unknown") {
		t.Fatalf("unexpected label/transport fallback: %s", line)
	}
}

func TestDiagnosticMotorStepUsesSessionWithSimulatedTransport(t *testing.T) {
	session := &Session{Protocol: "cs2+tcp"}
	sendCalls := 0
	session.motorStepSender = func(operation int) error {
		sendCalls++
		if operation != 1 {
			t.Fatalf("left mapped to operation %d, want 1", operation)
		}
		session.logReply(reply{
			cmd:        ptzReplyCommandID,
			body:       []byte(`{"ret":0,"angle":1,"token":"must-not-appear"}`),
			receivedAt: time.Now(),
		})
		return nil
	}
	result := session.DiagnosticMotorStep("left")
	line := result.SanitizedLine()
	if sendCalls != 1 || !result.WriteSucceeded() {
		t.Fatalf("diagnostic did not use one successful session write: %s", line)
	}
	for _, want := range []string{"camera=C201 transport=cs2+tcp", "cmd_sent_id=0x112", `payload={"operation":1}`, "reply_status=reply_received", "reply_cmd_id=0x113", "motion=not_measured"} {
		if !strings.Contains(line, want) {
			t.Errorf("session diagnostic line missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "must-not-appear") {
		t.Fatalf("session diagnostic leaked raw reply content: %s", line)
	}
}

func TestObservePTZStepSignalsImmediatelyAfterSuccessfulWrite(t *testing.T) {
	current := time.Unix(100, 0)
	written, observed := false, false
	result := observePTZStepWithCallback("C201", "cs2+tcp", 1,
		func() error { written = true; return nil },
		func() []reply { return nil },
		func() time.Time { return current },
		func(d time.Duration) {
			if !observed {
				t.Fatal("write callback must run before waiting for reply")
			}
			current = current.Add(d)
		},
		func() { observed = written })
	if !result.WriteSucceeded() || !observed {
		t.Fatalf("write callback status not observable: result=%s observed=%v", result.SanitizedLine(), observed)
	}
}
