package fs

import (
	"errors"
	"os"

	"github.com/spf13/afero"
)

// envDataDir is the environment variable used to resolve the default data directory.
const envDataDir = "PULSE_DATA_DIR"

// Config holds filesystem configuration for Pulse storage operations.
// The FS field uses afero.Fs as the interface contract, enabling transparent
// swapping between real OS filesystems and in-memory implementations for testing.
//
// Extension hook: callers can inject any afero.Fs implementation via WithFs
// to support custom storage backends (e.g., encrypted FS, remote mounts).
type Config struct {
	dataDir string
	fs      afero.Fs
}

// DataDir returns the resolved data directory path.
func (c *Config) DataDir() string {
	return c.dataDir
}

// Fs returns the configured filesystem.
func (c *Config) Fs() afero.Fs {
	return c.fs
}

// Option configures a Config during construction.
type Option func(*Config)

// WithDataDir sets an explicit data directory path.
// This takes precedence over the PULSE_DATA_DIR environment variable.
func WithDataDir(dir string) Option {
	return func(c *Config) {
		c.dataDir = dir
	}
}

// WithFs injects a custom afero.Fs implementation.
// When set, the filesystem is used as-is without wrapping in BasePathFs.
func WithFs(fs afero.Fs) Option {
	return func(c *Config) {
		c.fs = fs
	}
}

// New creates a configured filesystem with the given options.
// If no FS is injected, an OsFs rooted at the data directory is created.
// If no DataDir is set, it is not resolved from the environment (use Default for that).
func New(opts ...Option) (*Config, error) {
	cfg := &Config{}

	for _, opt := range opts {
		opt(cfg)
	}

	// If a custom FS was injected, use it directly.
	if cfg.fs != nil {
		return cfg, nil
	}

	// Default to OsFs rooted at the data directory.
	if cfg.dataDir == "" {
		return nil, errors.New("data directory required: set WithDataDir or use Default()")
	}

	cfg.fs = afero.NewBasePathFs(afero.NewOsFs(), cfg.dataDir)
	return cfg, nil
}

// Default returns an OsFs rooted at the directory specified by the
// PULSE_DATA_DIR environment variable. Returns an error if the variable
// is empty or unset.
func Default() (*Config, error) {
	dir := os.Getenv(envDataDir)
	if dir == "" {
		return nil, errors.New("PULSE_DATA_DIR environment variable is not set; set it or use New(WithDataDir(...))")
	}

	return New(WithDataDir(dir))
}

// NewMemMap returns a Config backed by an in-memory MemMapFs.
// Intended for testing; the DataDir is empty.
func NewMemMap() *Config {
	return &Config{
		fs: afero.NewMemMapFs(),
	}
}
