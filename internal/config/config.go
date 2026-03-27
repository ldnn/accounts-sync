package config

import (
	"log"
	"os"
	"path/filepath"
	"strings"
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

	// 尝试从 Secret 挂载的文件加载配置
	if cfg.AppID == "" || cfg.AppSecret == "" || cfg.BaseURL == "" {
		secretPath := os.Getenv("SECRET_MOUNT_PATH")
		if secretPath == "" {
			secretPath = "/etc/secrets" // 默认 Secret 挂载路径
		}

		secretConfig, err := loadFromSecret(secretPath)
		if err != nil {
			log.Printf("Warning: failed to load config from secret: %v\n", err)
		} else {
			if cfg.AppID == "" {
				cfg.AppID = secretConfig.AppID
			}
			if cfg.AppSecret == "" {
				cfg.AppSecret = secretConfig.AppSecret
			}
			if cfg.BaseURL == "" {
				cfg.BaseURL = secretConfig.BaseURL
			}
		}
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

func loadFromSecret(path string) (*Config, error) {
	cfg := &Config{}

	// 读取 app-id
	if data, err := os.ReadFile(filepath.Join(path, "app-id")); err == nil {
		cfg.AppID = strings.TrimSpace(string(data))
	}

	// 读取 app-secret
	if data, err := os.ReadFile(filepath.Join(path, "app-secret")); err == nil {
		cfg.AppSecret = strings.TrimSpace(string(data))
	}

	// 读取 remote-base-url
	if data, err := os.ReadFile(filepath.Join(path, "remote-base-url")); err == nil {
		cfg.BaseURL = strings.TrimSpace(string(data))
	}

	return cfg, nil
}
