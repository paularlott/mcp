package pool

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// HTTPPool interface for HTTP client providers
// Implementations can provide connection pooling, custom timeouts, etc.
type HTTPPool interface {
	GetHTTPClient() *http.Client
}

// PoolConfig holds configuration for the default pool in the mcp package
type PoolConfig struct {
	// InsecureSkipVerify allows self-signed certificates
	// WARNING: This should be false in production for security
	InsecureSkipVerify bool

	// Connection pool settings
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	IdleConnTimeout     time.Duration

	// Default timeout for requests
	Timeout time.Duration
}

// DefaultPoolConfig returns sensible defaults (secure by default)
// Optimized for long-lived AI/MCP connections
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		InsecureSkipVerify:  false, // Reject self-signed certs by default (secure)
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		Timeout:             5 * time.Minute, // Longer timeout for AI/MCP operations
	}
}

// Default pool implementation in mcp package
var (
	defaultPool     HTTPPool
	poolOnce        sync.Once
	poolConfig      *PoolConfig
	poolConfigMutex sync.RWMutex // Protects poolConfig for SetPoolConfig/GetPoolConfig
)

// SetPool sets the global pool for mcp clients
// This allows external pools to be injected
func SetPool(pool HTTPPool) {
	defaultPool = pool
}

// GetPool returns the global pool (creates default if nil)
func GetPool() HTTPPool {
	if defaultPool == nil {
		poolOnce.Do(func() {
			// Create mcp's own pool with configured or default settings
			defaultPool = newDefaultPoolImpl()
		})
	}
	return defaultPool
}

// SetPoolConfig sets the configuration for the mcp package's default pool
// Must be called before any HTTP calls are made (before GetPool)
func SetPoolConfig(config *PoolConfig) {
	poolConfigMutex.Lock()
	defer poolConfigMutex.Unlock()
	poolConfig = config
}

// GetPoolConfig returns the current pool configuration
// Returns a copy to prevent external modification of internal state
func GetPoolConfig() PoolConfig {
	poolConfigMutex.RLock()
	defer poolConfigMutex.RUnlock()

	if poolConfig == nil {
		return *DefaultPoolConfig()
	}
	return *poolConfig
}

// NewPool creates a new HTTP pool with the given configuration.
// The config is merged with DefaultPoolConfig() - any nil/zero values in config
// will use the corresponding defaults. This ensures consistency across pools
// while allowing specific overrides (e.g., InsecureSkipVerify).
//
// Example:
//
//	// Create an insecure pool for internal services with self-signed certs
//	// All other settings (timeouts, connection limits) inherit from defaults
//	insecurePool := pool.NewPool(&pool.PoolConfig{
//	    InsecureSkipVerify: true,
//	})
func NewPool(config *PoolConfig) HTTPPool {
	// Start with defaults
	defaults := DefaultPoolConfig()
	
	// Merge config with defaults - only override non-zero values
	merged := &PoolConfig{
		InsecureSkipVerify:  config.InsecureSkipVerify, // bool, so always use provided value
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		IdleConnTimeout:     config.IdleConnTimeout,
		Timeout:             config.Timeout,
	}
	
	// Apply defaults for zero values
	if merged.MaxIdleConns == 0 {
		merged.MaxIdleConns = defaults.MaxIdleConns
	}
	if merged.MaxIdleConnsPerHost == 0 {
		merged.MaxIdleConnsPerHost = defaults.MaxIdleConnsPerHost
	}
	if merged.IdleConnTimeout == 0 {
		merged.IdleConnTimeout = defaults.IdleConnTimeout
	}
	if merged.Timeout == 0 {
		merged.Timeout = defaults.Timeout
	}
	
	return createPoolWithConfig(merged)
}

// DefaultPool is the default HTTP pool implementation
type DefaultPool struct {
	httpClient *http.Client
}

// newDefaultPoolImpl creates a new default HTTP pool with configured or default settings
func newDefaultPoolImpl() *DefaultPool {
	poolConfigMutex.RLock()
	cfg := poolConfig
	poolConfigMutex.RUnlock()

	if cfg == nil {
		cfg = DefaultPoolConfig()
	}

	return createPoolWithConfig(cfg)
}

// createPoolWithConfig creates a pool with the given configuration
func createPoolWithConfig(cfg *PoolConfig) *DefaultPool {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify,
			MinVersion:         tls.VersionTLS13,
		},
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		ForceAttemptHTTP2:   true,
	}

	http2.ConfigureTransport(transport)

	return &DefaultPool{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
	}
}

// GetHTTPClient returns the shared HTTP client
func (p *DefaultPool) GetHTTPClient() *http.Client {
	return p.httpClient
}

// Ensure DefaultPool implements HTTPPool
var _ HTTPPool = (*DefaultPool)(nil)
