package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	API struct {
		BaseURL      string `mapstructure:"base_url"`
		ClientID     string `mapstructure:"client_id"`
		ClientSecret string `mapstructure:"client_secret"`
		DirectoryID  string `mapstructure:"directory_id"`
		Timeout      int    `mapstructure:"timeout"`
		RetryCount   int    `mapstructure:"retry_count"`
	} `mapstructure:"api"`

	Backup struct {
		Directories   []string `mapstructure:"directories"`
		ExcludePatterns []string `mapstructure:"exclude_patterns"`
		CheckInterval int      `mapstructure:"check_interval"`
		MaxFileSize   int64    `mapstructure:"max_file_size"`
		Concurrent    int      `mapstructure:"concurrent"`
		Compression   struct {
			Enabled bool   `mapstructure:"enabled"`
			Level   int    `mapstructure:"level"`
			Format  string `mapstructure:"format"`
		} `mapstructure:"compression"`
		Encryption struct {
			Enabled  bool   `mapstructure:"enabled"`
			Password string `mapstructure:"password"`
		} `mapstructure:"encryption"`
	} `mapstructure:"backup"`

	Report struct {
		Directory string `mapstructure:"directory"`
		Format    string `mapstructure:"format"`
		Retention int    `mapstructure:"retention"`
	} `mapstructure:"report"`

	Log struct {
		Level  string `mapstructure:"level"`
		File   string `mapstructure:"file"`
		Format string `mapstructure:"format"`
	} `mapstructure:"log"`

	Database struct {
		Path      string `mapstructure:"path"`
		Retention int    `mapstructure:"retention"`
	} `mapstructure:"database"`
}

var cfg *Config

func Load(configPath string) (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	
	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		
		configDir := filepath.Join(home, ".koneksi-backup")
		viper.AddConfigPath(configDir)
		viper.AddConfigPath(".")
	}

	viper.SetDefault("api.base_url", "https://koneksi-tyk-gateway-3rvca.ondigitalocean.app")
	viper.SetDefault("api.directory_id", "6839deb70fe80fe0747654b2") // Default directory
	viper.SetDefault("api.timeout", 30)
	viper.SetDefault("api.retry_count", 3)
	viper.SetDefault("backup.check_interval", 300)
	viper.SetDefault("backup.max_file_size", 1073741824) // 1GB
	viper.SetDefault("backup.concurrent", 5)
	viper.SetDefault("backup.compression.enabled", false)
	viper.SetDefault("backup.compression.level", 6) // 1-9, 6 is default gzip
	viper.SetDefault("backup.compression.format", "gzip")
	viper.SetDefault("backup.encryption.enabled", false)
	viper.SetDefault("backup.encryption.password", "")
	viper.SetDefault("report.directory", "./reports")
	viper.SetDefault("report.format", "json")
	viper.SetDefault("report.retention", 30)
	viper.SetDefault("log.level", "info")
	viper.SetDefault("log.format", "json")
	viper.SetDefault("database.path", "./backup.db")
	viper.SetDefault("database.retention", 90)

	viper.SetEnvPrefix("KONEKSI")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	cfg = &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return cfg, nil
}

func Get() *Config {
	if cfg == nil {
		panic("config not loaded")
	}
	return cfg
}

func (c *Config) Validate() error {
	if c.API.ClientID == "" {
		return fmt.Errorf("API client ID is required. Set it in config.yaml or use KONEKSI_API_CLIENT_ID environment variable")
	}
	if c.API.ClientSecret == "" {
		return fmt.Errorf("API client secret is required. Set it in config.yaml or use KONEKSI_API_CLIENT_SECRET environment variable")
	}
	if len(c.Backup.Directories) == 0 {
		return fmt.Errorf("at least one backup directory must be specified")
	}
	return nil
}