package notification

import (
	"context"
	"encoding/json"
	"social-media-app/api/auth"
	"social-media-app/api/models"
	"social-media-app/api/ws"
	"social-media-app/config"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
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

// CreateNotification creates a new notification and sends it via WebSocket
func (h *NotificationHandler) CreateNotification(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var req struct {
		ReceiverID string `json:"receiverId" validate:"required"`
		Type       string `json:"type" validate:"required"`
		Message    string `json:"message" validate:"required"`
		PostID     string `json:"postId"`
		CommentID  string `json:"commentId"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	// Validate receiver ID
	if _, err := uuid.Parse(req.ReceiverID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid receiverId format"})
	}

	// Verify receiver exists
	var receiver models.User
	if err := h.db.Where("id = ?", req.ReceiverID).First(&receiver).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Receiver not found"})
	}

	// Convert string to *string for PostID and CommentID
	var postID, commentID *string
	if req.PostID != "" {
		postID = &req.PostID
	}
	if req.CommentID != "" {
		commentID = &req.CommentID
	}

	notification := models.Notification{
		ID:         uuid.New().String(),
		UserID:     req.ReceiverID,
		Type:       req.Type,
		FromUserID: userID,
		PostID:     postID,
		CommentID:  commentID,
		Message:    req.Message,
		Read:       false,
		CreatedAt:  time.Now(),
	}

	if err := h.db.Create(&notification).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Invalidate cache
	h.redisClient.Del(context.Background(), "notifications:"+req.ReceiverID)

	// Prepare notification payload
	notificationJSON, _ := json.Marshal(fiber.Map{
		"id":           notification.ID,
		"type":         notification.Type,
		"fromUserId":   notification.FromUserID,
		"fromUserName": h.getUsername(notification.FromUserID),
		"postId":       notification.PostID,
		"commentId":    notification.CommentID,
		"message":      notification.Message,
		"read":         notification.Read,
		"createdAt":    notification.CreatedAt,
	})

	// Publish to Redis for WebSocket
	h.redisClient.Publish(context.Background(), "notification:"+req.ReceiverID, notificationJSON)

	// Send via WebSocket
	ws.SendNotification(req.ReceiverID, fiber.Map{
		"id":           notification.ID,
		"type":         notification.Type,
		"fromUserId":   notification.FromUserID,
		"fromUserName": h.getUsername(notification.FromUserID),
		"postId":       notification.PostID,
		"commentId":    notification.CommentID,
		"message":      notification.Message,
		"read":         notification.Read,
		"createdAt":    notification.CreatedAt,
	})

	return c.JSON(fiber.Map{"message": "Notification created", "notification": notification})
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
	notificationJSON, _ := json.Marshal(fiber.Map{
		"id":           notification.ID,
		"type":         notification.Type,
		"fromUserId":   notification.FromUserID,
		"fromUserName": h.getUsername(notification.FromUserID),
		"postId":       notification.PostID,
		"commentId":    notification.CommentID,
		"message":      notification.Message,
		"read":         notification.Read,
		"createdAt":    notification.CreatedAt,
	})
	h.redisClient.Publish(context.Background(), "notification:"+userID, notificationJSON)

	// Send via WebSocket
	ws.SendNotification(userID, fiber.Map{
		"id":           notification.ID,
		"type":         notification.Type,
		"fromUserId":   notification.FromUserID,
		"fromUserName": h.getUsername(notification.FromUserID),
		"postId":       notification.PostID,
		"commentId":    notification.CommentID,
		"message":      notification.Message,
		"read":         notification.Read,
		"createdAt":    notification.CreatedAt,
	})

	return c.JSON(fiber.Map{"message": "Notification marked as read"})
}

// getUsername fetches the username for a given user ID
func (h *NotificationHandler) getUsername(userID string) string {
	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return ""
	}
	return user.Username
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
	notification.Post("/", auth.JWTMiddleware(cfg), handler.CreateNotification)
}