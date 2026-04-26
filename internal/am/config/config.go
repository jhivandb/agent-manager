package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Path            string
	CurrentInstance string              `yaml:"current_instance"`
	Instances       map[string]Instance `yaml:"instances"`
}

type Instance struct {
	URL            string     `yaml:"url"`
	TokenURL       string     `yaml:"token_url"`
	CurrentOrg     string     `yaml:"current_org,omitempty"`
	CurrentProject string     `yaml:"current_project,omitempty"`
	Auth           AuthConfig `yaml:"auth,omitempty"`
}

type AuthConfig struct {
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	AccessToken  string `yaml:"access_token,omitempty"`
	RefreshToken string `yaml:"refresh_token,omitempty"`
	ExpiresAt    string `yaml:"expires_at,omitempty"`
}

func (c *Config) Current() (*Instance, error) {
	currentInstance := c.CurrentInstance
	if currentInstance == "" {
		return nil, fmt.Errorf("No Instance selected")
	}

	instance, ok := c.Instances[currentInstance]
	if !ok {
		return nil, fmt.Errorf("Current Instance not available")
	}
	return &instance, nil
}

func (c *Config) AddInstance(name string, inst Instance) {
	// May need some validation
	if c.Instances == nil {
		c.Instances = map[string]Instance{}
	}
	c.Instances[name] = inst
	c.CurrentInstance = name
}

func (c *Config) Load(path string) (*Config, error) {
	f, err := os.Open(path)

	if err != nil {
		return nil, fmt.Errorf("Failed to open config file")
	}
	defer f.Close()
	var cfg Config

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err = dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("Unable to decode Config file")
	}
	return &cfg, nil
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()

	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".am", "config"), nil
}

func Save(path string, cfg Config)
