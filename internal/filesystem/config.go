package filesystem

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ValidToolNames is the set of recognized tool names for allowlist validation.
var ValidToolNames = map[string]bool{
	"list_folder":      true,
	"read_file":        true,
	"write_file":       true,
	"remove_file":      true,
	"patch_file":       true,
	"create_folder":    true,
	"remove_folder":    true,
	"stat_file":        true,
	"hash_file":        true,
	"permissions_file": true,
	"copy":             true,
	"move":             true,
	"grep":             true,
	"glob":             true,
}

// Config holds the mcp-filesystem server configuration.
type Config struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	MaxFullReadSize int64  `yaml:"max_full_read_size"`
	Roots           []Root `yaml:"roots"`
}

// Root defines a named root directory with an allowlist of tools.
type Root struct {
	Name         string   `yaml:"name"`
	Path         string   `yaml:"path"`
	AllowedTools []string `yaml:"allowed_tools"`
}

// ResolvedRoot is a validated root with resolved absolute path and fast tool lookup.
type ResolvedRoot struct {
	Name         string
	RealPath     string
	AllowedTools map[string]bool
}

// LoadConfig reads and validates a YAML configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 8091
	}
	if cfg.MaxFullReadSize == 0 {
		cfg.MaxFullReadSize = 1048576 // 1MB
	}

	return &cfg, nil
}

// ResolveRoots validates and resolves all configured roots.
func ResolveRoots(cfg *Config) (map[string]*ResolvedRoot, error) {
	if len(cfg.Roots) == 0 {
		return nil, fmt.Errorf("no roots configured")
	}

	roots := make(map[string]*ResolvedRoot, len(cfg.Roots))
	for i, r := range cfg.Roots {
		if r.Name == "" {
			return nil, fmt.Errorf("roots[%d]: name is required", i)
		}
		if _, exists := roots[r.Name]; exists {
			return nil, fmt.Errorf("duplicate root name: %s", r.Name)
		}
		if r.Path == "" {
			return nil, fmt.Errorf("roots[%d] %q: path is required", i, r.Name)
		}

		// Resolve to absolute real path
		realPath, err := filepath.EvalSymlinks(r.Path)
		if err != nil {
			return nil, fmt.Errorf("roots[%d] %q: resolve path %s: %w", i, r.Name, r.Path, err)
		}
		realPath, err = filepath.Abs(realPath)
		if err != nil {
			return nil, fmt.Errorf("roots[%d] %q: abs path: %w", i, r.Name, err)
		}

		// Verify it exists and is a directory
		info, err := os.Stat(realPath)
		if err != nil {
			return nil, fmt.Errorf("roots[%d] %q: path %s: %w", i, r.Name, r.Path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("roots[%d] %q: path %s is not a directory", i, r.Name, r.Path)
		}

		// Build allowed tools set
		allowed := make(map[string]bool)
		for _, tool := range r.AllowedTools {
			if tool == "*" {
				for name := range ValidToolNames {
					allowed[name] = true
				}
				break
			}
			if !ValidToolNames[tool] {
				return nil, fmt.Errorf("roots[%d] %q: unknown tool %q in allowed_tools", i, r.Name, tool)
			}
			allowed[tool] = true
		}

		roots[r.Name] = &ResolvedRoot{
			Name:         r.Name,
			RealPath:     realPath,
			AllowedTools: allowed,
		}
	}

	return roots, nil
}
