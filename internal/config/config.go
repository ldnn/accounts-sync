package config

import (
	"log"
	"os"
)

type Config struct {
	AppID     string
	AppSecret string
	BaseURL   string
}

func Load() *Config {
	cfg := &Config{
		AppID:     os.Getenv("APP_ID"),
		AppSecret: os.Getenv("APP_SECRET"),
		BaseURL:   os.Getenv("REMOTE_BASE_URL"),
	}

	if cfg.AppID == "" {
		log.Fatal("APP_ID not set")
	}
	if cfg.AppSecret == "" {
		log.Fatal("APP_SECRET not set")
	}
	if cfg.BaseURL == "" {
		log.Fatal("REMOTE_BASE_URL not set")
	}

	return cfg
}
