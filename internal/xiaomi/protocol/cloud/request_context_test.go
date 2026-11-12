package cloud

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestRequestContextStopsBeforeSendingWhenCanceled(t *testing.T) {
	c := &Cloud{client: &http.Client{}, cookies: "session=test", ssecurity: []byte("test-secret")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.RequestContext(ctx, "http://127.0.0.1:1", "/test", "{}", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestContext error = %v, want context.Canceled", err)
	}
}
