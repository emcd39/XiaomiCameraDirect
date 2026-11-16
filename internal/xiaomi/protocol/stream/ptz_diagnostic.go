package stream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const (
	ptzRequestCommandID  = 0x112
	ptzReplyCommandID    = 0x113
	ptzReplyTimeout      = 2 * time.Second
	ptzReplyPollInterval = 10 * time.Millisecond
)

var ptzReplyFields = []string{"ret", "angle", "elevation", "xAngle", "yAngle", "xStep", "yStep"}

// PTZDiagnosticResult contains only the sanitized request/reply evidence needed
// to distinguish a transport write from a decoded MISS motor response.
type PTZDiagnosticResult struct {
	sentAt           time.Time
	writeCompletedAt time.Time
	replyReceivedAt  time.Time
	cameraLabel      string
	transport        string
	operation        int
	writeStatus      string
	replyStatus      string
	replyCommandID   uint32
	replyLength      int
	replyLatency     time.Duration
	replyFields      map[string]json.Number
	otherReplyCount  int
	lateReplyCount   int
}

// DiagnosticMotorStep sends one known direction on this session's existing
// command channel, then observes the decoded reply log for at most two seconds.
// Call it on the same Session instance that is feeding the video decoder.
func (s *Session) DiagnosticMotorStep(direction string) PTZDiagnosticResult {
	return s.DiagnosticMotorStepObserved(direction, nil)
}

// DiagnosticMotorStepObserved invokes onWrite immediately after the MISS
// command has been written and before it waits for a decoded 0x113 reply.
func (s *Session) DiagnosticMotorStepObserved(direction string, onWrite func()) PTZDiagnosticResult {
	operation, err := motorOperation(direction)
	if err != nil {
		return PTZDiagnosticResult{cameraLabel: "C201", transport: safePTZTransport(s.Protocol), writeStatus: "not_attempted", replyStatus: "not_waited"}
	}
	return s.DiagnosticMotorOperationObserved(operation, "C201", onWrite)
}

// DiagnosticMotorOperationObserved sends a profile-selected operation and
// observes the ordinary MISS reply without logging model or device identity.
func (s *Session) DiagnosticMotorOperationObserved(operation int, cameraLabel string, onWrite func()) PTZDiagnosticResult {
	return observePTZStepWithCallback(cameraLabel, safePTZTransport(s.Protocol), operation,
		func() error { return s.StepOperation(operation) },
		s.takeReplies, time.Now, time.Sleep, onWrite)
}

// SanitizedLine emits an allowlisted one-line timeline. It never emits a raw
// reply body, camera identifier, URL, key, token, or transport error text.
func (r PTZDiagnosticResult) SanitizedLine() string {
	sentAt := "none"
	if !r.sentAt.IsZero() {
		sentAt = r.sentAt.UTC().Format(time.RFC3339Nano)
	}
	commandID := "none"
	payload := "none"
	if r.operation >= 1 && r.operation <= 4 {
		commandID = fmt.Sprintf("0x%x", ptzRequestCommandID)
		payload = fmt.Sprintf(`{"operation":%d}`, r.operation)
	}
	replyID := "none"
	if r.replyCommandID != 0 {
		replyID = fmt.Sprintf("0x%x", r.replyCommandID)
	}
	receivedAt, latency := "none", "none"
	if !r.replyReceivedAt.IsZero() {
		receivedAt = r.replyReceivedAt.UTC().Format(time.RFC3339Nano)
		latency = strconv.FormatInt(r.replyLatency.Milliseconds(), 10)
	}
	fields, _ := json.Marshal(r.replyFields)
	if len(fields) == 0 || string(fields) == "null" {
		fields = []byte("{}")
	}
	timeout := "none"
	if r.replyStatus == "no_reply" {
		timeout = strconv.FormatInt(ptzReplyTimeout.Milliseconds(), 10)
	}
	writeCompletedAt := "none"
	if !r.writeCompletedAt.IsZero() {
		writeCompletedAt = r.writeCompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("PTZ_DIAG sent_at=%s write_completed_at=%s camera=%s transport=%s cmd_sent_id=%s payload=%s write_status=%s reply_status=%s reply_cmd_id=%s reply_len=%d reply_fields=%s reply_received_at=%s reply_latency_ms=%s reply_timeout_ms=%s other_reply_count=%d late_reply_count=%d motion=not_measured",
		sentAt, writeCompletedAt, safePTZLabel(r.cameraLabel), safePTZTransport(r.transport), commandID,
		payload, safePTZStatus(r.writeStatus), safePTZStatus(r.replyStatus), replyID,
		r.replyLength, fields, receivedAt, latency, timeout, r.otherReplyCount, r.lateReplyCount)
}

func observePTZStep(
	cameraLabel, transport string,
	operation int,
	send func() error,
	drain func() []reply,
	now func() time.Time,
	wait func(time.Duration),
) PTZDiagnosticResult {
	return observePTZStepWithCallback(cameraLabel, transport, operation, send, drain, now, wait, nil)
}

func observePTZStepWithCallback(
	cameraLabel, transport string,
	operation int,
	send func() error,
	drain func() []reply,
	now func() time.Time,
	wait func(time.Duration),
	onWrite func(),
) PTZDiagnosticResult {
	result := PTZDiagnosticResult{
		cameraLabel: safePTZLabel(cameraLabel),
		transport:   safePTZTransport(transport),
		operation:   operation,
		writeStatus: "write_error",
		replyStatus: "not_waited",
	}
	if operation < 1 || operation > 4 || send == nil || drain == nil || now == nil || wait == nil {
		result.operation = 0
		result.writeStatus = "not_attempted"
		return result
	}

	// Remove stale replies before the write so an earlier command cannot be
	// mistaken for this step's response.
	_ = drain()
	result.sentAt = now()
	if err := send(); err != nil {
		result.writeCompletedAt = now()
		return result
	}
	result.writeCompletedAt = now()
	result.writeStatus = "write_ok"
	result.replyStatus = "no_reply"
	if onWrite != nil {
		onWrite()
	}
	deadline := result.writeCompletedAt.Add(ptzReplyTimeout)

	for {
		for _, reply := range drain() {
			if reply.receivedAt.Before(result.sentAt) {
				continue
			}
			if reply.cmd == ptzReplyCommandID {
				if reply.receivedAt.After(deadline) {
					result.lateReplyCount++
					result.replyCommandID = reply.cmd
					result.replyLength = len(reply.body)
					continue
				}
				result.replyStatus = "reply_received"
				result.replyCommandID = reply.cmd
				result.replyLength = len(reply.body)
				result.replyReceivedAt = reply.receivedAt
				result.replyLatency = reply.receivedAt.Sub(result.sentAt)
				result.replyFields = sanitizePTZReplyFields(reply.body)
				return result
			}
			result.otherReplyCount++
			result.replyCommandID = reply.cmd
			result.replyLength = len(reply.body)
		}

		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return result
		}
		if remaining > ptzReplyPollInterval {
			remaining = ptzReplyPollInterval
		}
		wait(remaining)
	}
}

// WriteSucceeded reports the transport write result without exposing its error
// text, which may contain connection details.
func (r PTZDiagnosticResult) WriteSucceeded() bool {
	return r.writeStatus == "write_ok"
}

func sanitizePTZReplyFields(body []byte) map[string]json.Number {
	var values map[string]json.RawMessage
	if json.Unmarshal(body, &values) != nil {
		var encoded string
		if json.Unmarshal(body, &encoded) != nil || json.Unmarshal([]byte(encoded), &values) != nil {
			return nil
		}
	}
	fields := make(map[string]json.Number)
	for _, name := range ptzReplyFields {
		raw, ok := values[name]
		if !ok {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			continue
		}
		number, ok := value.(json.Number)
		if !ok {
			continue
		}
		if _, err := strconv.ParseFloat(number.String(), 64); err == nil {
			fields[name] = number
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func safePTZLabel(value string) string {
	if value == "C201" || value == "MISS" {
		return value
	}
	return "unknown"
}

func safePTZTransport(value string) string {
	if value == "cs2+tcp" || value == "cs2+udp" {
		return value
	}
	return "unknown"
}

func safePTZStatus(value string) string {
	if value == "write_ok" || value == "write_error" || value == "not_attempted" || value == "reply_received" || value == "no_reply" || value == "not_waited" {
		return value
	}
	return "unknown"
}
