package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RuleConfig 成组的匹配和重写规则
type RuleConfig struct {
	Name            string            `yaml:"name"`
	Match           []string          `yaml:"match"`
	Overwrite       map[string]string `yaml:"overwrite"`
	Plugins         []string          `yaml:"plugins"`
	ResponsePlugins []string          `yaml:"response_plugins"`
}

// Config 配置文件结构
type Config struct {
	Rules           []RuleConfig      `yaml:"rules"`
	Match           []string          `yaml:"match"`
	Overwrite       map[string]string `yaml:"overwrite"`
	Plugins         []string          `yaml:"plugins"`
	ResponsePlugins []string          `yaml:"response_plugins"`
	Upstream        string            `yaml:"upstream"`
	Port            int               `yaml:"port"`
	Verbose         bool              `yaml:"verbose"`
	VVerbose        bool              `yaml:"vverbose"`
	DumpTraffic     bool              `yaml:"dump-traffic"`
	LogFile         string            `yaml:"log-file"`
}

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &cfg, nil
}
