package config

import (
	"social-media-app/api/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitDB(cfg *Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  cfg.DatabaseURL,
		PreferSimpleProtocol: true, // disables statement caching
		
		
	}), &gorm.Config{})
	
	if err != nil {
		return nil, err
	}
	db.AutoMigrate(&models.User{}, &models.Post{}, &models.Chat{}, &models.Message{}, &models.Product{})
	return db, nil
}