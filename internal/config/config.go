package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

type Config struct {
	Addr                 string        `env:"RULESHIFT_ADDR" envDefault:":8080" validate:"required"`
	OperationsAddr       string        `env:"RULESHIFT_OPERATIONS_ADDR" envDefault:"127.0.0.1:9091" validate:"required"`
	Env                  string        `env:"RULESHIFT_ENV" envDefault:"dev"`
	DatabaseURL          string        `env:"RULESHIFT_DATABASE_URL" validate:"required_with=DeveloperAPIKey"`
	DatabaseAdminURL     string        `env:"RULESHIFT_DATABASE_ADMIN_URL"`
	ModuleDatabasePrefix string        `env:"RULESHIFT_MODULE_DATABASE_PREFIX" envDefault:"ruleshift_module_" validate:"required"`
	DeveloperID          string        `env:"RULESHIFT_DEVELOPER_ID" envDefault:"default" validate:"required"`
	DeveloperName        string        `env:"RULESHIFT_DEVELOPER_NAME" envDefault:"Default developer" validate:"required"`
	DeveloperAPIKey      string        `env:"RULESHIFT_DEVELOPER_API_KEY"`
	KubeconfigPath       string        `env:"RULESHIFT_KUBECONFIG"`
	MaxMessageBytes      int           `env:"RULESHIFT_MAX_MESSAGE_BYTES" envDefault:"65536" validate:"gt=0"`
	RoomInputQueueSize   int           `env:"RULESHIFT_ROOM_INPUT_QUEUE_SIZE" envDefault:"1024" validate:"gt=0"`
	SessionSendQueueSize int           `env:"RULESHIFT_SESSION_SEND_QUEUE_SIZE" envDefault:"256" validate:"gt=0"`
	AuthTimeout          time.Duration `env:"RULESHIFT_AUTH_TIMEOUT" envDefault:"5s" validate:"gt=0"`
	ReadTimeout          time.Duration `env:"RULESHIFT_READ_TIMEOUT" envDefault:"30s" validate:"gt=0"`
	WriteTimeout         time.Duration `env:"RULESHIFT_WRITE_TIMEOUT" envDefault:"30s" validate:"gt=0"`
	ShutdownTimeout      time.Duration `env:"RULESHIFT_SHUTDOWN_TIMEOUT" envDefault:"10s" validate:"gt=0"`
	EnableMetrics        bool          `env:"RULESHIFT_ENABLE_METRICS" envDefault:"true"`
	EnablePprof          bool          `env:"RULESHIFT_ENABLE_PPROF" envDefault:"false"`
	PublicRoomRefKey     string        `env:"RULESHIFT_PUBLIC_ROOM_REF_KEY" validate:"omitempty,min=32"`
	QueueDegradedRatio   float64       `env:"RULESHIFT_QUEUE_DEGRADED_RATIO" envDefault:"0.8" validate:"gt=0,lte=1"`
}

var configValidator = validator.New(validator.WithRequiredStructEnabled())

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("parse Ruleshift environment: %w", err)
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := configValidator.Struct(c); err != nil {
		return fmt.Errorf("validate Ruleshift configuration: %w", err)
	}
	return nil
}
