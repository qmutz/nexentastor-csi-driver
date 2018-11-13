package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Config - driver config from file
type Config struct {
	Address                string `yaml:"restIp"`
	Username               string `yaml:"username"`
	Password               string `yaml:"password"`
	DefaultDataset         string `yaml:"defaultDataset,omitempty"`
	DefaultDataIP          string `yaml:"defaultDataIp,omitempty"`
	DefaultNfsMountOptions string `yaml:"defaultNfsMountOptions,omitempty"`
	Debug                  bool   `yaml:"debug,omitempty"`

	filePath    string
	lastMobTime time.Time
}

// GetFilePath - get filepath of found config file
func (c *Config) GetFilePath() string {
	return c.filePath
}

// Refresh - read and validate config, return `true` if config has been changed
func (c *Config) Refresh() (changed bool, err error) {
	if c.filePath == "" {
		return false, fmt.Errorf("Cannot read config file, filePath not specified")
	}

	fileInfo, err := os.Stat(c.filePath)
	if err != nil {
		return false, fmt.Errorf("Cannot get stats for '%v' config file: %v", c.filePath, err)
	}

	changed = c.lastMobTime != fileInfo.ModTime()

	if changed {
		c.lastMobTime = fileInfo.ModTime()

		content, err := ioutil.ReadFile(c.filePath)
		if err != nil {
			return changed, fmt.Errorf("Cannot read '%v' config file: %v", c.filePath, err)
		}

		if err := yaml.Unmarshal(content, c); err != nil {
			return changed, fmt.Errorf("Cannot parse yaml in '%v' config file: %v", c.filePath, err)
		}

		if err := c.Validate(); err != nil {
			return changed, err
		}
	}

	return changed, nil
}

// Validate - validate current config
func (c *Config) Validate() error {
	var errors []string

	//TODO validate address schema too
	if c.Address == "" {
		errors = append(errors, fmt.Sprintf("parameter 'restIp' is missed"))
	}
	if c.Username == "" {
		errors = append(errors, fmt.Sprintf("parameter 'username' is missed"))
	}
	if c.Password == "" {
		errors = append(errors, fmt.Sprintf("parameter 'password' is missed"))
	}

	if len(errors) != 0 {
		return fmt.Errorf("Bad format, fix following issues: %v", strings.Join(errors, ", "))
	}

	return nil
}

// New - find config file and create config instance
func New(lookUpDir string) (*Config, error) {
	// look up for config file
	configFilePath := ""
	err := filepath.Walk(lookUpDir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".yaml" || ext == ".yml" {
			configFilePath = path
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Cannot read config directory '%v'", lookUpDir)
	} else if configFilePath == "" {
		return nil, fmt.Errorf("Cannot find .yaml config file in '%v' directory", lookUpDir)
	}

	// read config file
	config := &Config{filePath: configFilePath}
	if _, err := config.Refresh(); err != nil {
		return nil, fmt.Errorf("Cannot refresh config from file '%v': %v", configFilePath, err)
	}

	return config, nil
}
