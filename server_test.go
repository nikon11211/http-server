package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testConfig() *Config {
	return &Config{
		Service:             "test-service",
		Address:             "127.0.0.1:0",
		MetricsAddress:      "127.0.0.1:0",
		IdleTimeout:         120,
		ReadTimeout:         30,
		WriteTimeout:        30,
		WriteContextTimeout: 5,
		ShutdownTimeout:     5,
		KeepAlive:           true,
		RateLimit: RateLimitConfig{
			IsDisable: true,
			Limit:     100,
			Burst:     20,
			ExpireIn:  60,
		},
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		want   func(*testing.T, *Server)
	}{
		{
			name:   "rate limiter enabled",
			config: testConfig(),
			want: func(t *testing.T, s *Server) {
				assert.NotNil(t, s.echo)
				assert.Equal(t, "127.0.0.1:0", s.Addr())
				assert.Equal(t, "127.0.0.1:0", s.MetricsAddr())
				assert.Empty(t, s.Routers())
			},
		},
		{
			name: "custom logger",
			config: func() *Config {
				cfg := testConfig()
				cfg.RateLimit.IsDisable = false
				return cfg
			}(),
			want: func(t *testing.T, s *Server) {
				assert.NotNil(t, s)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.config, WithLogger(NoopLogger{}))
			assert.NotNil(t, s)
			tt.want(t, s)
		})
	}
}

func TestNewPanicsOnNilConfig(t *testing.T) {
	assert.Panics(t, func() { New(nil) })
}

func TestAddRouter(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	r := s.AddRouter("/api")
	assert.NotNil(t, r)
	assert.Equal(t, "/api", r.Name())
	assert.Contains(t, s.Routers(), "/api")
}

func TestStartStop(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	err := s.Start()
	require.NoError(t, err)
	assert.NotEqual(t, "127.0.0.1:0", s.Addr())
	assert.NotEqual(t, "127.0.0.1:0", s.MetricsAddr())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, s.Stop(ctx))
}

func TestStartBindFailure(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer blocker.Close()

	cfg := testConfig()
	cfg.Address = blocker.Addr().String()
	s := New(cfg, WithLogger(NoopLogger{}))
	err = s.Start()
	require.Error(t, err)
}

func TestStartMetricsBindFailure(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer blocker.Close()

	cfg := testConfig()
	cfg.MetricsAddress = blocker.Addr().String()
	s := New(cfg, WithLogger(NoopLogger{}))
	err = s.Start()
	require.Error(t, err)
}

func TestRequestRoundTrip(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	s.echo.GET("/hello", func(c echo.Context) error {
		return c.String(http.StatusOK, "world")
	})
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	req, err := http.NewRequest(http.MethodGet, "http://"+s.Addr()+"/hello", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.com")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "world", string(body))
	assert.NotEmpty(t, resp.Header.Get(echo.HeaderXRequestID))
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestNotFound(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	resp := doGet(t, "http://"+s.Addr()+"/missing")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestContextTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.WriteContextTimeout = 1
	s := New(cfg, WithLogger(NoopLogger{}))
	s.echo.GET("/slow", func(c echo.Context) error {
		select {
		case <-c.Request().Context().Done():
			return c.Request().Context().Err()
		case <-time.After(3 * time.Second):
			return c.String(http.StatusOK, "too slow")
		}
	})
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	start := time.Now()
	resp := doGet(t, "http://"+s.Addr()+"/slow")
	defer resp.Body.Close()
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
}

func TestRateLimiter(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit = RateLimitConfig{Limit: 1, Burst: 1, ExpireIn: 60}
	s := New(cfg, WithLogger(NoopLogger{}))
	s.echo.GET("/ping", func(c echo.Context) error {
		return c.String(http.StatusOK, "pong")
	})
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	url := "http://" + s.Addr() + "/ping"

	first := doGet(t, url)
	first.Body.Close()
	assert.Equal(t, http.StatusOK, first.StatusCode)

	second := doGet(t, url)
	second.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, second.StatusCode)
}

func TestMetricsEndpoint(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	s.echo.GET("/hit", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	resp := doGet(t, "http://"+s.Addr()+"/hit")
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	metricsResp, err := http.Get("http://" + s.MetricsAddr() + "/metrics")
	require.NoError(t, err)
	defer metricsResp.Body.Close()
	assert.Equal(t, http.StatusOK, metricsResp.StatusCode)
	body, _ := io.ReadAll(metricsResp.Body)
	assert.Contains(t, string(body), "test_service_echo_requests_total")
}

func TestWithTracing(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	defer tp.Shutdown(context.Background())

	s := New(testConfig(), WithLogger(NoopLogger{}))
	s.WithTracing(tp)
	s.echo.GET("/traced", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	resp := doGet(t, "http://"+s.Addr()+"/traced")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("traceparent"))
}

func TestStopTwice(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	require.NoError(t, s.Start())
	require.NoError(t, s.Stop(context.Background()))
	require.NoError(t, s.Stop(context.Background()))
}

func TestStopCanceledContext(t *testing.T) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	require.NoError(t, s.Start())

	for _, addr := range []string{s.Addr(), s.MetricsAddr()} {
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
	}
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.Stop(ctx)
	require.Error(t, err)
}

func TestServeMainListenerClosed(t *testing.T) {
	logger := &channelLogger{ch: make(chan string, 10)}
	s := New(testConfig(), WithLogger(logger))
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	require.NoError(t, s.mainListener.Close())
	assert.Eventually(t, func() bool {
		for _, m := range logger.snapshot() {
			if strings.Contains(m, "main server stopped unexpectedly") {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
}

func TestServeMetricsListenerClosed(t *testing.T) {
	logger := &channelLogger{ch: make(chan string, 10)}
	s := New(testConfig(), WithLogger(logger))
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	require.NoError(t, s.metricsListener.Close())
	assert.Eventually(t, func() bool {
		for _, m := range logger.snapshot() {
			if strings.Contains(m, "metrics server stopped unexpectedly") {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
}

func TestRateLimiterHandlers(t *testing.T) {
	cfg := buildRateLimiterConfig(testConfig())

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	c := e.NewContext(req, httptest.NewRecorder())

	id, err := cfg.IdentifierExtractor(c)
	assert.NoError(t, err)
	assert.Equal(t, "127.0.0.1", id)

	err = cfg.ErrorHandler(c, fmt.Errorf("store failure"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, c.Response().Status)

	err = cfg.DenyHandler(c, "127.0.0.1", middleware.ErrRateLimitExceeded)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, c.Response().Status)
}

type channelLogger struct {
	ch chan string
}

func (l *channelLogger) Debug(msg string) { l.ch <- "debug: " + msg }
func (l *channelLogger) Info(msg string)  { l.ch <- "info: " + msg }
func (l *channelLogger) Warn(msg string)  { l.ch <- "warn: " + msg }
func (l *channelLogger) Error(msg string) { l.ch <- "error: " + msg }

func (l *channelLogger) snapshot() []string {
	var out []string
	for {
		select {
		case m := <-l.ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

func doGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func BenchmarkServerRoundTrip(b *testing.B) {
	s := New(testConfig(), WithLogger(NoopLogger{}))
	s.echo.GET("/bench", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	if err := s.Start(); err != nil {
		b.Fatal(err)
	}
	defer s.Stop(context.Background())

	url := "http://" + s.Addr() + "/bench"
	client := &http.Client{Timeout: 5 * time.Second}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("unexpected status: %d", resp.StatusCode)
		}
	}
}

func BenchmarkRequestLogger(b *testing.B) {
	logger := &countingLogger{}
	s := New(testConfig(), WithLogger(logger))
	s.echo.GET("/log", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	if err := s.Start(); err != nil {
		b.Fatal(err)
	}
	defer s.Stop(context.Background())

	url := "http://" + s.Addr() + "/log"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

type countingLogger struct {
	count atomic.Int64
}

func (c *countingLogger) Debug(msg string) { c.count.Add(1) }
func (c *countingLogger) Info(msg string)  { c.count.Add(1) }
func (c *countingLogger) Warn(msg string)  { c.count.Add(1) }
func (c *countingLogger) Error(msg string) { c.count.Add(1) }

func TestLoggingMiddleware(t *testing.T) {
	logger := &countingLogger{}
	s := New(testConfig(), WithLogger(logger))
	s.echo.GET("/ok", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	s.echo.GET("/bad", func(c echo.Context) error { return fmt.Errorf("boom") })
	require.NoError(t, s.Start())
	defer s.Stop(context.Background())

	doGet(t, "http://"+s.Addr()+"/ok").Body.Close()
	doGet(t, "http://"+s.Addr()+"/bad").Body.Close()

	time.Sleep(50 * time.Millisecond)
	assert.Greater(t, logger.count.Load(), int64(1))
}

func TestDefaultSlogLogger(t *testing.T) {
	l := newSlogLogger()
	l.Debug("debug msg")
	l.Info("info msg")
	l.Warn("warn msg")
	l.Error("error msg")
}

func BenchmarkRateLimiter(b *testing.B) {
	cfg := testConfig()
	cfg.RateLimit = RateLimitConfig{Limit: 100000, Burst: 100000, ExpireIn: 60}
	s := New(cfg, WithLogger(NoopLogger{}))
	s.echo.GET("/rl", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	if err := s.Start(); err != nil {
		b.Fatal(err)
	}
	defer s.Stop(context.Background())

	url := "http://" + s.Addr() + "/rl"
	body := bytes.NewBuffer(nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest(http.MethodGet, url, body)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}
