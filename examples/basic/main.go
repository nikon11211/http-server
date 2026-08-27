package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	httpserver "github.com/nikon11211/http-server"
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
			Limit:     100,
			Burst:     20,
			ExpireIn:  60,
		},
	}

	server := httpserver.New(config)

	api := server.AddRouter("/api")
	api.GET("/hello", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "hello from http-server"})
	})

	if err := server.Start(); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.ShutdownTimeout)*time.Second)
		defer cancel()
		if err := server.Stop(ctx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}()

	fmt.Println("server listening on", server.Addr(), "metrics on", server.MetricsAddr())
	select {}
}
