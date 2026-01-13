package mcp

// RedisSessionManager provides distributed session storage using Redis.
//
// # Why This Is Commented Out
//
// This is a REFERENCE IMPLEMENTATION for SessionManager using Redis.
// It is intentionally commented out to avoid adding Redis as a required dependency
// for users who don't need it. Most deployments should use JWTSessionManager
// which is stateless and requires no external dependencies.
//
// # When To Use Redis Sessions
//
// Use Redis sessions when you need:
//   - Session revocation (logout, security incidents)
//   - Session listing (admin dashboards)
//   - Custom session metadata storage
//   - Strict session lifecycle control
//
// # How To Use
//
// 1. Copy this implementation to your project
// 2. Add the Redis dependency: go get github.com/redis/go-redis/v9
// 3. Uncomment and adapt the code below
//
// Example:
//
//	import "github.com/redis/go-redis/v9"
//
//	rdb := redis.NewClient(&redis.Options{
//		Addr: "localhost:6379",
//	})
//	sessionMgr := NewRedisSessionManager(rdb, 30*time.Minute)
//	server.SetSessionManager(sessionMgr)

/*
import "github.com/redis/go-redis/v9"

type RedisSessionManager struct {
	client      *redis.Client
	sessionTTL  time.Duration
}

// NewRedisSessionManager creates a new Redis-based session manager
func NewRedisSessionManager(client *redis.Client, sessionTTL time.Duration) *RedisSessionManager {
	return &RedisSessionManager{
		client:     client,
		sessionTTL: sessionTTL,
	}
}

func (m *RedisSessionManager) generateSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (m *RedisSessionManager) CreateSession(ctx context.Context, protocolVersion string) (string, error) {
	sessionID, err := m.generateSessionID()
	if err != nil {
		return "", err
	}

	// Store session data in Redis with TTL
	key := fmt.Sprintf("mcp:session:%s", sessionID)
	protoKey := fmt.Sprintf("mcp:session:%s:protocol", sessionID)

	pipe := m.client.Pipeline()
	pipe.Set(ctx, key, time.Now().Unix(), m.sessionTTL)
	pipe.Set(ctx, protoKey, protocolVersion, m.sessionTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("failed to create session in Redis: %w", err)
	}

	return sessionID, nil
}

func (m *RedisSessionManager) ValidateSession(ctx context.Context, sessionID string) (bool, error) {
	key := fmt.Sprintf("mcp:session:%s", sessionID)

	// Check if session exists
	exists, err := m.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check session: %w", err)
	}

	if exists == 0 {
		return false, nil
	}

	// Update last access time and refresh TTL
	now := time.Now().Unix()
	if err := m.client.Set(ctx, key, now, m.sessionTTL).Err(); err != nil {
		return false, fmt.Errorf("failed to update session: %w", err)
	}

	return true, nil
}

func (m *RedisSessionManager) GetProtocolVersion(ctx context.Context, sessionID string) (string, error) {
	protoKey := fmt.Sprintf("mcp:session:%s:protocol", sessionID)

	version, err := m.client.Get(ctx, protoKey).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get protocol version: %w", err)
	}

	return version, nil
}

func (m *RedisSessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	key := fmt.Sprintf("mcp:session:%s", sessionID)
	protoKey := fmt.Sprintf("mcp:session:%s:protocol", sessionID)

	pipe := m.client.Pipeline()
	pipe.Del(ctx, key)
	pipe.Del(ctx, protoKey)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

func (m *RedisSessionManager) CleanupExpiredSessions(ctx context.Context, maxIdleTime time.Duration) error {
	// Redis automatically expires keys based on TTL, so this is a no-op
	// Could implement a scan-based cleanup if needed for additional housekeeping
	return nil
}
*/
