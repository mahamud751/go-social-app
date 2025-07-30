package chat

import (
	"context"
	"encoding/json"
	"social-media-app/api/auth"
	"social-media-app/api/models"
	"social-media-app/config"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type CreateChatRequest struct {
	SenderID   string `json:"senderId" validate:"required"`
	ReceiverID string `json:"receiverId" validate:"required"`
}

type ChatHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewChatHandler(db *gorm.DB, redisClient *redis.Client) *ChatHandler {
	return &ChatHandler{db: db, redisClient: redisClient}
}

func (h *ChatHandler) CreateChat(c *fiber.Ctx) error {
	var req CreateChatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	// Validate UUIDs
	if _, err := uuid.Parse(req.SenderID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid senderId format"})
	}
	if _, err := uuid.Parse(req.ReceiverID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid receiverId format"})
	}

	// Verify users exist
	var sender, receiver models.User
	if err := h.db.Where("id = ?", req.SenderID).First(&sender).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Sender not found"})
	}
	if err := h.db.Where("id = ?", req.ReceiverID).First(&receiver).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Receiver not found"})
	}

	// Check if chat already exists
	var existingChat models.Chat
	if err := h.db.Where("members @> ARRAY[?, ?]::uuid[]", req.SenderID, req.ReceiverID).First(&existingChat).Error; err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"message": "Chat already exists", "chat": existingChat})
	}

	chat := models.Chat{
		Members: models.UUIDArray{req.SenderID, req.ReceiverID},
	}

	if err := h.db.Create(&chat).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	chatJSON, _ := json.Marshal(chat)
	redisKey := "chat:" + chat.ID
	h.redisClient.Set(context.Background(), redisKey, chatJSON, 0)

	return c.JSON(chat)
}

func (h *ChatHandler) UserChats(c *fiber.Ctx) error {
	userID := c.Params("userId")
	var chats []models.Chat
	if err := h.db.Where("? = ANY(members)", userID).Find(&chats).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	return c.JSON(chats)
}

func (h *ChatHandler) FindChat(c *fiber.Ctx) error {
	firstID := c.Params("firstId")
	secondID := c.Params("secondId")
	var chat models.Chat
	if err := h.db.Where("members @> ARRAY[?, ?]::uuid[]", firstID, secondID).First(&chat).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Chat not found"})
	}
	return c.JSON(chat)
}

func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewChatHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	chat := api.Group("/chat")
	chat.Post("/", auth.JWTMiddleware(cfg), handler.CreateChat)
	chat.Get("/:userId", handler.UserChats)
	chat.Get("/find/:firstId/:secondId", handler.FindChat)
}