package story

import (
	"context"
	"encoding/json"
	"social-media-app/api/auth"
	"social-media-app/api/models"
	"social-media-app/api/ws"
	"social-media-app/config"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"time"
)

type CreateStoryRequest struct {
	UserID string `json:"userId" validate:"required"`
	Text   string `json:"text"`
	Image  string `json:"image"`
	Color  string `json:"color" validate:"required"` // Background color for text
}

type StoryHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewStoryHandler(db *gorm.DB, redisClient *redis.Client) *StoryHandler {
	return &StoryHandler{db, redisClient}
}

func (h *StoryHandler) CreateStory(c *fiber.Ctx) error {
	var req CreateStoryRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	authUserID := c.Locals("user_id").(string)
	if req.UserID != authUserID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Cannot create story for another user"})
	}

	var user models.User
	if err := h.db.Where("id = ?", req.UserID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	story := models.Story{
		UserID: req.UserID,
		Text:   req.Text,
		Image:  req.Image,
		Color:  req.Color,
	}

	if err := h.db.Create(&story).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to create story: " + err.Error()})
	}

	// Cache the story
	storyJSON, _ := json.Marshal(story)
	h.redisClient.Set(context.Background(), "story:"+story.ID, storyJSON, 24*time.Hour)

	// Notify followers
	followers := user.Followers
	storyMap := map[string]interface{}{
		"id":        story.ID,
		"userId":    story.UserID,
		"text":      story.Text,
		"image":     story.Image,
		"color":     story.Color,
		"createdAt": story.CreatedAt,
		"updatedAt": story.UpdatedAt,
	}
	ws.SendStoryCreated(followers, storyMap)

	return c.JSON(story)
}

func (h *StoryHandler) GetStories(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	followingIDs := []string(user.Following)
	followingIDs = append(followingIDs, userID) // Include own stories

	var stories []models.Story
	now := time.Now().Add(-24 * time.Hour)
	if err := h.db.Where("user_id IN ? AND created_at > ?", followingIDs, now).Find(&stories).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	return c.JSON(stories)
}

func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewStoryHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	story := api.Group("/story")
	story.Post("/", auth.JWTMiddleware(cfg), handler.CreateStory)
	story.Get("/", auth.JWTMiddleware(cfg), handler.GetStories)
}