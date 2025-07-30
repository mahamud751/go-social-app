package message

import (
	"context"
	"encoding/json"
	"social-media-app/api/auth"
	"social-media-app/api/models"
	"social-media-app/config"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type MessageRequest struct {
	ChatID   string `json:"chatId" validate:"required"`
	SenderID string `json:"senderId" validate:"required"`
	Text     string `json:"text" validate:"required"`
}

type ActiveUser struct {
	UserID   string
	SocketID string
}

type MessageHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
	activeUsers map[string]ActiveUser
}

func NewMessageHandler(db *gorm.DB, redisClient *redis.Client) *MessageHandler {
	return &MessageHandler{
		db:          db,
		redisClient: redisClient,
		activeUsers: make(map[string]ActiveUser),
	}
}

func (h *MessageHandler) AddMessage(c *fiber.Ctx) error {
    var req MessageRequest
    if err := c.BodyParser(&req); err != nil {
        return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
    }

    // Validate UUIDs
    if _, err := uuid.Parse(req.ChatID); err != nil {
        return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid chatId format"})
    }
    if _, err := uuid.Parse(req.SenderID); err != nil {
        return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid senderId format"})
    }

    // Verify chat exists
    var chat models.Chat
    if err := h.db.Where("id = ?", req.ChatID).First(&chat).Error; err != nil {
        return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Chat not found"})
    }

    // Verify sender exists
    var sender models.User
    if err := h.db.Where("id = ?", req.SenderID).First(&sender).Error; err != nil {
        return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Sender not found"})
    }

    message := models.Message{
        ChatID:   req.ChatID,
        SenderID: req.SenderID,
        Text:     req.Text,
    }
    if err := h.db.Create(&message).Error; err != nil {
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
    }

    messageJSON, _ := json.Marshal(message)
    h.redisClient.Publish(context.Background(), "chat:"+req.ChatID, messageJSON)
    return c.JSON(message)
}

func (h *MessageHandler) GetMessages(c *fiber.Ctx) error {
	chatID := c.Params("chatId")
	var messages []models.Message
	if err := h.db.Where("chat_id = ?", chatID).Find(&messages).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	return c.JSON(messages)
}

func (h *MessageHandler) HandleWebSocket(c *websocket.Conn) {
	userID := c.Query("userId")
	if userID == "" {
		c.Close()
		return
	}

	h.activeUsers[userID] = ActiveUser{UserID: userID, SocketID: c.RemoteAddr().String()}
	h.redisClient.Publish(context.Background(), "users", h.getActiveUsersJSON())

	ctx := context.Background()
	channels := []string{}
	var chatIDs []string
	if err := h.db.Model(&models.Chat{}).Where("? = ANY(members)", userID).Pluck("id", &chatIDs).Error; err != nil {
		c.WriteJSON(fiber.Map{"message": "Failed to fetch chats"})
		c.Close()
		return
	}
	for _, chatID := range chatIDs {
		channels = append(channels, "chat:"+chatID)
	}
	pubsub := h.redisClient.Subscribe(ctx, channels...)
	defer pubsub.Close()

	go func() {
		ch := pubsub.Channel()
		for msg := range ch {
			var message models.Message
			if err := json.Unmarshal([]byte(msg.Payload), &message); err == nil {
				c.WriteJSON(message)
			}
		}
	}()

	for {
		var req MessageRequest
		if err := c.ReadJSON(&req); err != nil {
			delete(h.activeUsers, userID)
			h.redisClient.Publish(context.Background(), "users", h.getActiveUsersJSON())
			break
		}

		message := models.Message{
			ChatID:   req.ChatID,
			SenderID: req.SenderID,
			Text:     req.Text,
		}
		if err := h.db.Create(&message).Error; err != nil {
			c.WriteJSON(fiber.Map{"message": "Failed to save message"})
			continue
		}

		messageJSON, _ := json.Marshal(message)
		h.redisClient.Publish(context.Background(), "chat:"+req.ChatID, messageJSON)
	}
}

func (h *MessageHandler) getActiveUsersJSON() string {
	usersJSON, _ := json.Marshal(h.activeUsers)
	return string(usersJSON)
}

func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewMessageHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	message := api.Group("/message")
	message.Post("/", auth.JWTMiddleware(cfg), handler.AddMessage)
	message.Get("/:chatId", handler.GetMessages)
	api.Get("/ws", websocket.New(handler.HandleWebSocket))
}