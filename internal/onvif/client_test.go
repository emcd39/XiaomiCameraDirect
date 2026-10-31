package onvif

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPTZDirectionsAndPresetEscaping(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("unexpected method %s", r.Method)
		}
		raw := make([]byte, 8192)
		n, _ := r.Body.Read(raw)
		bodies = append(bodies, string(raw[:n]))
		w.Header().Set("Content-Type", "application/soap+xml")
		_, _ = w.Write([]byte(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><ok/></s:Body></s:Envelope>`))
	}))
	defer server.Close()
	c := New()
	for _, direction := range []string{"left", "right", "up", "down"} {
		if err := c.Step(context.Background(), server.URL, "garden", direction, 0.1); err != nil {
			t.Fatal(err)
		}
	}
	values := []string{`x="-0.100" y="0.000"`, `x="0.100" y="0.000"`, `x="0.000" y="0.100"`, `x="0.000" y="-0.100"`}
	for i, want := range values {
		if !strings.Contains(bodies[i], want) {
			t.Fatalf("%s missing %s", bodies[i], want)
		}
	}
	if err := c.Preset(context.Background(), server.URL, "garden", "A&B"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[4], "A&amp;B") {
		t.Fatal("preset escaping")
	}
}

func TestSOAPFaultFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><s:Fault/></s:Body></s:Envelope>`))
	}))
	defer server.Close()
	if err := New().Stop(context.Background(), server.URL, "garden"); err == nil {
		t.Fatal("expected fault")
	}
}
