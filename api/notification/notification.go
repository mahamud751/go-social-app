package notification

import (
	"context"
	"encoding/json"
	"social-media-app/api/auth"
	"social-media-app/api/models"
	"social-media-app/config"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type NotificationHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewNotificationHandler(db *gorm.DB, redisClient *redis.Client) *NotificationHandler {
	return &NotificationHandler{db, redisClient}
}

// GetNotifications retrieves all notifications for the current user
func (h *NotificationHandler) GetNotifications(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	// Check Redis cache first
	cached, err := h.redisClient.Get(context.Background(), "notifications:"+userID).Result()
	if err == nil {
		var notifications []models.Notification
		if err := json.Unmarshal([]byte(cached), &notifications); err == nil {
			return c.JSON(notifications)
		}
	}

	// Fetch from database
	var notifications []models.Notification
	if err := h.db.Where("user_id = ?", userID).Order("created_at desc").Find(&notifications).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Fetch from_user usernames
	var fromUserIDs []string
	for _, notification := range notifications {
		fromUserIDs = append(fromUserIDs, notification.FromUserID)
	}
	var users []models.User
	if len(fromUserIDs) > 0 {
		if err := h.db.Where("id IN ?", fromUserIDs).Find(&users).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
		}
	}
	userMap := make(map[string]string)
	for _, user := range users {
		userMap[user.ID] = user.Username
	}

	response := []fiber.Map{}
	for _, notification := range notifications {
		response = append(response, fiber.Map{
			"id":           notification.ID,
			"type":         notification.Type,
			"fromUserId":   notification.FromUserID,
			"fromUserName": userMap[notification.FromUserID],
			"postId":       notification.PostID,
			"commentId":    notification.CommentID,
			"message":      notification.Message,
			"read":         notification.Read,
			"createdAt":    notification.CreatedAt,
		})
	}

	// Cache the result
	notificationsJSON, _ := json.Marshal(response)
	h.redisClient.Set(context.Background(), "notifications:"+userID, notificationsJSON, 3600)

	return c.JSON(response)
}

// MarkNotificationAsRead marks a notification as read
func (h *NotificationHandler) MarkNotificationAsRead(c *fiber.Ctx) error {
	notificationID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var notification models.Notification
	if err := h.db.Where("id = ? AND user_id = ?", notificationID, userID).First(&notification).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Notification not found"})
	}

	notification.Read = true
	if err := h.db.Save(&notification).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Invalidate cache
	h.redisClient.Del(context.Background(), "notifications:"+userID)

	// Publish updated notification to Redis for WebSocket
	notificationJSON, _ := json.Marshal(notification)
	h.redisClient.Publish(context.Background(), "notification:"+userID, notificationJSON)

	return c.JSON(fiber.Map{"message": "Notification marked as read"})
}

// Setup configures the notification routes
func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewNotificationHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	notification := api.Group("/notification")
	notification.Get("/", auth.JWTMiddleware(cfg), handler.GetNotifications)
	notification.Put("/:id/read", auth.JWTMiddleware(cfg), handler.MarkNotificationAsRead)
}