package httpserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

type Server struct {
	echo            *echo.Echo
	config          *Config
	logger          Logger
	groups          map[string]*Router
	metricsSrv      *http.Server
	registry        *prometheus.Registry
	mainListener    net.Listener
	metricsListener net.Listener
	mainAddr        string
	metricsAddr     string
	mu              sync.Mutex
}

type Option func(*options)

type options struct {
	logger Logger
}

func WithLogger(logger Logger) Option {
	return func(o *options) {
		o.logger = logger
	}
}

func New(config *Config, opts ...Option) *Server {
	const op = "httpserver.New"

	if config == nil {
		panic(op + ": config cannot be nil")
	}

	o := &options{logger: newSlogLogger()}
	for _, opt := range opts {
		opt(o)
	}

	e := echo.New()
	e.HideBanner = true
	registry := prometheus.NewRegistry()
	e.Server.Addr = config.Address
	e.Server.ReadHeaderTimeout = time.Duration(config.ReadTimeout) * time.Second
	e.Server.ReadTimeout = time.Duration(config.ReadTimeout) * time.Second
	e.Server.WriteTimeout = time.Duration(config.WriteTimeout) * time.Second
	e.Server.IdleTimeout = time.Duration(config.IdleTimeout) * time.Second
	e.Server.SetKeepAlivesEnabled(config.KeepAlive)

	e.Use(middleware.ContextTimeoutWithConfig(middleware.ContextTimeoutConfig{
		Timeout: time.Duration(config.WriteContextTimeout) * time.Second,
		ErrorHandler: func(err error, c echo.Context) error {
			return ErrTimeout
		},
	}))
	e.Use(middleware.Recover())
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Generator: uuid.NewString,
	}))
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{http.MethodOptions, http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodPost, http.MethodDelete},
		AllowCredentials: true,
		AllowHeaders:     []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderXRequestedWith, echo.HeaderContentLength, echo.HeaderAcceptEncoding, echo.HeaderXCSRFToken, echo.HeaderAuthorization},
		MaxAge:           86400,
	}))
	e.Use(echoprometheus.NewMiddlewareWithConfig(echoprometheus.MiddlewareConfig{
		Namespace:  config.Service,
		Registerer: registry,
	}))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:    true,
		LogURI:       true,
		LogRequestID: true,
		LogMethod:    true,
		LogError:     true,
		HandleError:  true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			if v.Error == nil {
				o.logger.Info(fmt.Sprintf("request completed: %s %s status=%d request_id=%s",
					v.Method, v.URI, v.Status, v.RequestID))
			} else {
				o.logger.Error(fmt.Sprintf("request failed: %s %s status=%d error=%v request_id=%s",
					v.Method, v.URI, v.Status, v.Error, v.RequestID))
			}
			return nil
		},
	}))

	if !config.RateLimit.IsDisable {
		o.logger.Info(fmt.Sprintf("rate limiter enabled: %+v", config.RateLimit))
		e.Use(middleware.RateLimiterWithConfig(buildRateLimiterConfig(config)))
	} else {
		o.logger.Info("rate limiter disabled")
	}

	return &Server{
		echo:        e,
		config:      config,
		logger:      o.logger,
		groups:      make(map[string]*Router),
		metricsSrv:  &http.Server{ReadHeaderTimeout: 10 * time.Second},
		registry:    registry,
		mainAddr:    config.Address,
		metricsAddr: config.MetricsAddress,
	}
}

func buildRateLimiterConfig(config *Config) middleware.RateLimiterConfig {
	store := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
		Rate:      rate.Limit(config.RateLimit.Limit),
		Burst:     config.RateLimit.Burst,
		ExpiresIn: time.Duration(config.RateLimit.ExpireIn) * time.Second,
	})
	return middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store:   store,
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(c echo.Context, err error) error {
			return c.JSON(http.StatusForbidden, nil)
		},
		DenyHandler: func(c echo.Context, identifier string, err error) error {
			return c.JSON(http.StatusTooManyRequests, echo.HTTPError{Code: http.StatusTooManyRequests, Message: "rate limit exceeded"})
		},
	}
}

func (s *Server) AddRouter(name string) *Router {
	group := &Router{name: name, Group: s.echo.Group(name)}
	s.mu.Lock()
	s.groups[name] = group
	s.mu.Unlock()
	return group
}

func (s *Server) Routers() map[string]*Router {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]*Router, len(s.groups))
	maps.Copy(result, s.groups)
	return result
}

func (s *Server) Start() error {
	const op = "httpserver.Start"

	mainListener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("%s: failed to bind main listener on %s: %w", op, s.config.Address, err)
	}
	s.mainListener = mainListener
	s.mainAddr = mainListener.Addr().String()

	metricsListener, err := net.Listen("tcp", s.config.MetricsAddress)
	if err != nil {
		_ = mainListener.Close()
		return fmt.Errorf("%s: failed to bind metrics listener on %s: %w", op, s.config.MetricsAddress, err)
	}
	s.metricsListener = metricsListener
	s.metricsAddr = metricsListener.Addr().String()

	s.metricsSrv = &http.Server{
		Addr:              s.metricsAddr,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}),
	}

	go func() {
		if err := s.serveMain(mainListener); err != nil {
			s.logger.Error(fmt.Sprintf("main server stopped unexpectedly: %v", err))
		}
	}()
	go func() {
		if err := s.metricsSrv.Serve(metricsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error(fmt.Sprintf("metrics server stopped unexpectedly: %v", err))
		}
	}()

	s.logger.Debug(fmt.Sprintf("binding complete: main=%s metrics=%s", s.mainAddr, s.metricsAddr))
	s.logger.Info(fmt.Sprintf("server started on %s, metrics on %s", s.mainAddr, s.metricsAddr))
	return nil
}

func (s *Server) serveMain(listener net.Listener) error {
	err := s.echo.Server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Addr() string { return s.mainAddr }

func (s *Server) MetricsAddr() string { return s.metricsAddr }

func (s *Server) Stop(ctx context.Context) error {
	const op = "httpserver.Stop"

	var result error
	if err := s.echo.Server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		result = fmt.Errorf("%s: failed to shut down main server: %w", op, err)
	}
	if err := s.metricsSrv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		result = fmt.Errorf("%s: failed to shut down metrics server: %w", op, err)
	}
	return result
}

func (s *Server) WithTracing(tp trace.TracerProvider, opts ...otelecho.Option) {
	const op = "httpserver.WithTracing"

	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	s.echo.Use(otelecho.Middleware(s.config.Service, append(
		[]otelecho.Option{
			otelecho.WithTracerProvider(tp),
			otelecho.WithPropagators(propagator),
		},
		opts...,
	)...))

	s.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			span := trace.SpanFromContext(c.Request().Context())
			if span != nil {
				span.SetAttributes(
					attribute.String("http.server", s.config.Service),
					attribute.String("http.address", s.config.Address),
				)
				sc := span.SpanContext()
				if sc.IsValid() {
					traceparent := fmt.Sprintf("00-%s-%s-01",
						sc.TraceID().String(),
						sc.SpanID().String(),
					)
					c.Response().Header().Set("traceparent", traceparent)
				}
			}
			return next(c)
		}
	})

	s.logger.Info(op + ": OpenTelemetry tracing enabled with W3C traceparent propagation")
}
