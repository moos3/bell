// Package config provides shared configuration loading for the DNS service.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the configuration for AlloyDB and DNS-related settings.
type Config struct {
	AlloyDB struct {
		Host     string `yaml:"host"`     // Database host (e.g., private IP)
		Port     string `yaml:"port"`     // Database port (e.g., 5432)
		User     string `yaml:"user"`     // Database user
		Password string `yaml:"password"` // Database password
		Database string `yaml:"database"` // Database name
		SSLMode  string `yaml:"sslmode"`  // SSL mode (disable, require, verify-ca, verify-full)
	} `yaml:"alloydb"`
	Zones struct {
		Directory               string `yaml:"directory"`                 // Directory containing zone files
		ReprocessThresholdHours int    `yaml:"reprocess_threshold_hours"` // Hours before reprocessing TLDs
		MaxConcurrent           int    `yaml:"max_concurrent"`            // Maximum concurrent TLD processing
		BatchSize               int    `yaml:"batch_size"`                // Batch size for record processing
	} `yaml:"zones"`
	DNSQuery struct {
		MaxConcurrent     int      `yaml:"max_concurrent"`      // Maximum concurrent DNS queries
		RetryDelaySeconds int      `yaml:"retry_delay_seconds"` // Delay between retries (seconds)
		BatchSize         int      `yaml:"batch_size"`          // Batch size for domain queries
		DNSServers        []string `yaml:"dns_servers"`         // List of DNS servers
	} `yaml:"dns_query"`
}

// LoadConfig reads and parses the YAML configuration file.
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %v", filePath, err)
	}
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %v\nEnsure YAML syntax is correct and all required fields are present", filePath, err)
	}
	if config.AlloyDB.Host == "" {
		return nil, fmt.Errorf("missing alloydb.host in %s", filePath)
	}
	if config.AlloyDB.User == "" {
		return nil, fmt.Errorf("missing alloydb.user in %s", filePath)
	}
	if config.AlloyDB.Database == "" {
		return nil, fmt.Errorf("missing alloydb.database in %s", filePath)
	}
	validSSLModes := map[string]bool{
		"disable":     true,
		"require":     true,
		"verify-ca":   true,
		"verify-full": true,
	}
	if !validSSLModes[config.AlloyDB.SSLMode] {
		return nil, fmt.Errorf("invalid alloydb.sslmode %s in %s; must be disable, require, verify-ca, or verify-full", config.AlloyDB.SSLMode, filePath)
	}
	return &config, nil
}
