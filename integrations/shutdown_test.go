package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/service"
	"github.com/vogo/vv/config"
	"github.com/vogo/vv/tools"
)

func TestIntegration_GracefulShutdown(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	testAgent := agent.NewCustomAgent(agent.Config{
		ID:          "test",
		Name:        "Test Agent",
		Description: "Test",
	}, func(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
		return &schema.RunResponse{}, nil
	})

	svc := service.New(
		service.Config{Addr: ":0"},
		service.WithToolRegistry(reg),
	)
	svc.RegisterAgent(testAgent)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Start(ctx)
	}()

	for range 50 {
		if svc.ListenAddr() != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if svc.ListenAddr() == "" {
		cancel()
		t.Fatal("server did not start")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}

func TestIntegration_GracefulShutdown_ServesBeforeCancel(t *testing.T) {
	reg, _ := tools.Register(config.ToolsConfig{BashTimeout: 30})

	testAgent := agent.NewCustomAgent(agent.Config{
		ID: "sig-test", Name: "Signal Test", Description: "test",
	}, func(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
		return &schema.RunResponse{}, nil
	})

	svc := service.New(service.Config{Addr: ":0"}, service.WithToolRegistry(reg))
	svc.RegisterAgent(testAgent)

	ts := httptest.NewServer(svc.Handler())
	healthResp, err := ts.Client().Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	_ = healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", healthResp.StatusCode)
	}
	ts.Close()

	ctx, stop := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- svc.Start(ctx)
	}()

	for range 50 {
		if svc.ListenAddr() != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if svc.ListenAddr() == "" {
		stop()
		t.Fatal("server did not start")
	}

	stop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}
