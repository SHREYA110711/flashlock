package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	AppPort int
	AppEnv  string

	// PostgreSQL
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
	DBMaxConns int32
	DBMinConns int32

	// Redis
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int
	RedisPoolSize int

	// Reservation Settings
	ReservationTTLSeconds int
}

func (c *Config) PostgresDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&pool_max_conns=%d&pool_min_conns=%d",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode, c.DBMaxConns, c.DBMinConns,
	)
}

func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%d", c.RedisHost, c.RedisPort)
}

func Load() (*Config, error) {
	// Try loading .env if present (ignore error if not present in production)
	_ = godotenv.Load()

	cfg := &Config{
		AppPort:               getEnvAsInt("APP_PORT", 8080),
		AppEnv:                getEnv("APP_ENV", "development"),
		DBHost:                getEnv("DB_HOST", "localhost"),
		DBPort:                getEnvAsInt("DB_PORT", 5432),
		DBUser:                getEnv("DB_USER", "flashlock"),
		DBPassword:            getEnv("DB_PASSWORD", "flashlock_secret_password"),
		DBName:                getEnv("DB_NAME", "flashlock"),
		DBSSLMode:             getEnv("DB_SSLMODE", "disable"),
		DBMaxConns:            int32(getEnvAsInt("DB_MAX_CONNS", 50)),
		DBMinConns:            int32(getEnvAsInt("DB_MIN_CONNS", 5)),
		RedisHost:             getEnv("REDIS_HOST", "localhost"),
		RedisPort:             getEnvAsInt("REDIS_PORT", 6379),
		RedisPassword:         getEnv("REDIS_PASSWORD", "flashlock_redis_secret"),
		RedisDB:               getEnvAsInt("REDIS_DB", 0),
		RedisPoolSize:         getEnvAsInt("REDIS_POOL_SIZE", 100),
		ReservationTTLSeconds: getEnvAsInt("RESERVATION_TTL_SECONDS", 600),
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	valStr := getEnv(key, "")
	if val, err := strconv.Atoi(valStr); err == nil {
		return val
	}
	return defaultVal
}
