<p align="center">
  <h1 align="center">🚀 HTTP-Server — Production-Ready HTTP Server for Go</h1>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/nikon11211/http-server">
    <img src="https://pkg.go.dev/badge/github.com/nikon11211/http-server.svg" alt="Go Reference"/>
  </a>
  <a href="https://goreportcard.com/report/github.com/nikon11211/http-server">
    <img src="https://goreportcard.com/badge/github.com/nikon11211/http-server" alt="Go Report Card"/>
  </a>
  <a href="https://github.com/nikon11211/http-server/actions/workflows/test.yaml">
    <img src="https://github.com/nikon11211/http-server/actions/workflows/test.yaml/badge.svg" alt="Tests"/>
  </a>
  <a href="https://codecov.io/gh/nikon11211/http-server">
    <img src="https://codecov.io/gh/nikon11211/http-server/branch/main/graph/badge.svg" alt="Coverage"/>
  </a>
  <a href="https://sonarcloud.io/summary/overall?id=nikon11211_http-server">
    <img src="https://sonarcloud.io/api/project_badges/measure?project=nikon11211_http-server&metric=coverage" alt="SonarCloud Coverage"/>
  </a>
  <a href="https://opensource.org/licenses/MIT">
    <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"/>
  </a>
  <a href="https://golang.org/">
    <img src="https://img.shields.io/badge/Go-%3E%3D%201.26-blue" alt="Go Version"/>
  </a>
</p>

<p align="center">
  <b>A batteries-included HTTP server for Go microservices</b><br/>
  <i>Structured slog JSON logging • Prometheus metrics • OpenTelemetry tracing • Per-IP rate limiting • CORS • Graceful shutdown</i>
</p>

---

## ✨ Why HTTP-Server?

Wiring up a production HTTP microservice means repeating the same boilerplate:
logging, metrics, tracing, rate limiting, request IDs, panic recovery and CORS.
This library bundles all of it on top of [Echo](https://github.com/labstack/echo)
so you can focus on your business logic:

- **Structured logging** via `log/slog` (JSON to stdout by default, plug in your own `Logger`)
- **Prometheus metrics** on a dedicated `/metrics` endpoint (per-service namespace, isolated registry)
- **OpenTelemetry tracing** with W3C `traceparent` propagation on every request
- **Per-IP rate limiting** (token bucket, configurable limit/burst/expiry)
- **CORS** with sane production defaults
- **Request IDs**, context timeouts, panic recovery
- **Named router groups** for clean API organization
- **Graceful shutdown** of both main and metrics listeners

## 📦 Installation

```bash
go get github.com/nikon11211/http-server
```

## 🚀 Quick Start

```go
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/nikon11211/http-server"
)

func main() {
	config := &httpserver.Config{
		Service:             "demo-service",
		Address:             ":8080",
		MetricsAddress:      ":9090",
		IdleTimeout:         120,
		ReadTimeout:         30,
		WriteTimeout:        30,
		WriteContextTimeout: 5,
		ShutdownTimeout:     5,
		KeepAlive:           true,
		RateLimit: httpserver.RateLimitConfig{
			IsDisable: false,
			Limit:     100, // requests per second per IP
			Burst:     20,
			ExpireIn:  60, // seconds
		},
	}

	server := httpserver.New(config)

	api := server.AddRouter("/api")
	api.GET("/hello", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "hello"})
	})

	if err := server.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
```

A complete runnable example lives in [`examples/basic`](examples/basic/main.go).

## 🏗️ Architecture

```
┌────────────────────────────────────────────────────────────┐
│                    Your Application                        │
├────────────────────────────────────────────────────────────┤
│                     httpserver.Server                     │
│                                                            │
│  ┌──────────┐ ┌───────────┐ ┌────────────┐ ┌────────────┐ │
│  │ Routers  │ │  Metrics  │ │  Tracing   │ │  Logging   │ │
│  │ (groups) │ │ (Prom)    │ │  (OTEL)    │ │  (slog)    │ │
│  └──────────┘ └───────────┘ └────────────┘ └────────────┘ │
│  Rate limiter (per IP) • CORS • Request ID • Recovery     │
│                            │                              │
│                      Echo / net/http                       │
└────────────────────────────────────────────────────────────┘
```

## ⚙️ Configuration

| Field               | Type   | Description                                           |
|---------------------|--------|-------------------------------------------------------|
| `Service`           | string | Service name used for metrics namespace and logs      |
| `Address`           | string | Main HTTP listen address (e.g. `":8080"`)             |
| `MetricsAddress`    | string | Prometheus metrics listen address (e.g. `":9090"`)    |
| `IdleTimeout`       | int    | Keep-alive idle timeout, seconds                      |
| `ReadTimeout`       | int    | Request read timeout, seconds                         |
| `WriteTimeout`      | int    | Response write timeout, seconds                       |
| `WriteContextTimeout` | int  | Per-request processing timeout, seconds               |
| `ShutdownTimeout`   | int    | Graceful shutdown timeout, seconds                    |
| `KeepAlive`         | bool   | Enable TCP keep-alive                                 |
| `RateLimit`         | struct | Per-IP rate limiter settings (see below)              |

`RateLimitConfig`:

| Field       | Type    | Description                              |
|-------------|---------|------------------------------------------|
| `IsDisable` | bool    | Disable the rate limiter entirely        |
| `Limit`     | float64 | Requests allowed per second per IP       |
| `Burst`     | int     | Maximum burst size                      |
| `ExpireIn`  | int64   | Bucket expiry, seconds                   |

## 🧩 Options

| Option              | Description                                              |
|---------------------|----------------------------------------------------------|
| `WithLogger(l)`     | Replace the default `slog` logger with your `Logger`     |
| `WithTracing(tp)`   | Enable OpenTelemetry tracing (call on the server later)  |

## ❌ Errors

| Sentinel error | Raised when                                  |
|----------------|----------------------------------------------|
| `ErrTimeout`    | A request exceeds `WriteContextTimeout`      |

## 📊 Observability

### Metrics

A dedicated Prometheus registry is created per server instance, so multiple
servers in one process never conflict. Metrics are exposed on
`http://<metrics-address>/metrics` with the service name as namespace:

```
my_service_echo_requests_total{method="GET",route="/api/hello"} 42
```

### Tracing

```go
server.WithTracing(tracerProvider)
```

Every request gets an OTel span, request attributes (`http.server`,
`http.address`) are attached, and the W3C `traceparent` header — built from
the live span — is propagated downstream for end-to-end correlation.

### Logging

Implement the 4-method `Logger` interface (or embed `NoopLogger`) and pass it:

```go
server := httpserver.New(config, httpserver.WithLogger(customLogger))
```

## 🧪 Testing & Benchmarks

The library ships with **100.0% statement coverage** (excluding `examples/`).
Unit tests run against `httptest` and ephemeral ports — no external services:

```bash
go test -race -coverprofile=coverage.txt -covermode=atomic $(go list ./... | grep -v /examples)
```

Run benchmarks:

```bash
go test -bench=. -benchmem -run '^$' .
```

| Benchmark                 | What it measures                              |
|---------------------------|-----------------------------------------------|
| `BenchmarkServerRoundTrip`| Full HTTP round trip through the middleware   |
| `BenchmarkRequestLogger`  | Structured request logging path              |
| `BenchmarkRateLimiter`    | Per-IP rate limiter admission                |

CI enforces the 100% gate, runs `go vet`, benchmarks and
[golangci-lint](https://golangci-lint.run) with a strict configuration, and
publishes coverage to Codecov and SonarCloud.

## 🤝 Contributing

We welcome contributions! Here's how you can help:

1. **Fork** the repository
2. **Create** a feature branch (`git checkout -b feature/amazing-feature`)
3. **Commit** your changes (`git commit -m 'Add amazing feature'`)
4. **Push** to the branch (`git push origin feature/amazing-feature`)
5. **Open** a Pull Request

Keep tests deterministic and preserve the 100% coverage gate.

## 📄 License

MIT License — see [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- [Echo](https://github.com/labstack/echo) — High-performance HTTP framework
- [echo-contrib/echoprometheus](https://github.com/labstack/echo-contrib) — Prometheus middleware
- [OpenTelemetry](https://opentelemetry.io/) — Distributed tracing standard
- [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate) — Rate limiting

---

<p align="center">
  <b>Made with ❤️ for the Go community</b><br/>
  <sub>Built for production, designed for microservices</sub>
</p>