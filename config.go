package httpserver

type Config struct {
	Service             string          `mapstructure:"service" validate:"required"`
	Address             string          `mapstructure:"address" validate:"required"`
	MetricsAddress      string          `mapstructure:"metrics_address" validate:"required"`
	IdleTimeout         int             `mapstructure:"idle_timeout" validate:"required"`
	ReadTimeout         int             `mapstructure:"read_timeout" validate:"required"`
	WriteTimeout        int             `mapstructure:"write_timeout" validate:"required"`
	WriteContextTimeout int             `mapstructure:"write_context_timeout" validate:"required"`
	ShutdownTimeout     int             `mapstructure:"shutdown_timeout" validate:"required"`
	KeepAlive           bool            `mapstructure:"keep_alive"`
	RateLimit           RateLimitConfig `mapstructure:"rate_limit" validate:"required"`
}

type RateLimitConfig struct {
	IsDisable bool    `mapstructure:"is_disable"`
	Limit     float64 `mapstructure:"limit" validate:"required"`
	Burst     int     `mapstructure:"burst" validate:"required"`
	ExpireIn  int64   `mapstructure:"expire_in" validate:"required"`
}
