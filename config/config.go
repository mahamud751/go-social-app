package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL   string
	DirectURL     string
	SupabaseURL   string
	SupabaseAnonKey string
	JWTSecret     string
	RedisURL      string
	Port          string
	CORSOrigin    string
}

func LoadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		return nil, err
	}

	return &Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		DirectURL:     os.Getenv("DIRECT_URL"),
		SupabaseURL:   os.Getenv("SUPABASE_URL"),
		SupabaseAnonKey: os.Getenv("SUPABASE_ANON_KEY"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		RedisURL:      os.Getenv("REDIS_URL"),
		Port:          os.Getenv("PORT"),
		CORSOrigin:    os.Getenv("CORS_ORIGIN"),
	}, nil
}