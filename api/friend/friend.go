package friend

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

type FriendRequestRequest struct {
	ReceiverID string `json:"receiverId" validate:"required"`
}

type FriendRequestHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewFriendRequestHandler(db *gorm.DB, redisClient *redis.Client) *FriendRequestHandler {
	return &FriendRequestHandler{db, redisClient}
}

// SendFriendRequest sends a friend request
func (h *FriendRequestHandler) SendFriendRequest(c *fiber.Ctx) error {
	var req FriendRequestRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	senderID := c.Locals("user_id").(string)
	if senderID == req.ReceiverID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Cannot send friend request to yourself"})
	}

	// Validate UUIDs
	if _, err := uuid.Parse(req.ReceiverID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid receiverId format"})
	}

	// Verify receiver exists
	var receiver models.User
	if err := h.db.Where("id = ?", req.ReceiverID).First(&receiver).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Receiver not found"})
	}

	// Check if request already exists
	var existing models.FriendRequest
	if err := h.db.Where("sender_id = ? AND receiver_id = ? AND status = ?", senderID, req.ReceiverID, "pending").First(&existing).Error; err == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Friend request already sent"})
	}

	friendRequest := models.FriendRequest{
		SenderID:   senderID,
		ReceiverID: req.ReceiverID,
		Status:     "pending",
	}

	if err := h.db.Create(&friendRequest).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Create notification for receiver
	var sender models.User
	var notification models.Notification
	if err := h.db.Where("id = ?", senderID).First(&sender).Error; err == nil {
		notification = models.Notification{
			UserID:     req.ReceiverID,
			Type:       "friend_request",
			FromUserID: senderID,
			Message:    sender.Username + " sent you a friend request",
			Read:       false,
		}
		h.db.Create(&notification)
	}

	// Publish notification via Redis for WebSocket
	notificationJSON, _ := json.Marshal(notification)
	h.redisClient.Publish(context.Background(), "notification:"+req.ReceiverID, notificationJSON)

	return c.JSON(fiber.Map{"message": "Friend request sent", "friendRequest": friendRequest})
}

// ListFriendRequests retrieves pending friend requests for the current user
func (h *FriendRequestHandler) ListFriendRequests(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var requests []models.FriendRequest
	if err := h.db.Where("receiver_id = ? AND status = ?", userID, "pending").Find(&requests).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Fetch sender usernames
	var senderIDs []string
	for _, req := range requests {
		senderIDs = append(senderIDs, req.SenderID)
	}
	var senders []models.User
	if len(senderIDs) > 0 {
		if err := h.db.Where("id IN ?", senderIDs).Find(&senders).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
		}
	}
	senderMap := make(map[string]string)
	for _, sender := range senders {
		senderMap[sender.ID] = sender.Username
	}

	response := []fiber.Map{}
	for _, req := range requests {
		response = append(response, fiber.Map{
			"id":         req.ID,
			"senderId":   req.SenderID,
			"senderName": senderMap[req.SenderID],
			"status":     req.Status,
			"createdAt":  req.CreatedAt,
		})
	}

	return c.JSON(response)
}

// ConfirmFriendRequest confirms a friend request
func (h *FriendRequestHandler) ConfirmFriendRequest(c *fiber.Ctx) error {
	requestID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var friendRequest models.FriendRequest
	if err := h.db.Where("id = ? AND receiver_id = ?", requestID, userID).First(&friendRequest).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Friend request not found"})
	}

	if friendRequest.Status != "pending" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Friend request already processed"})
	}

	tx := h.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Update friend request status
	friendRequest.Status = "accepted"
	if err := tx.Save(&friendRequest).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Add to Friends field
	var sender, receiver models.User
	if err := tx.Where("id = ?", friendRequest.SenderID).First(&sender).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Sender not found"})
	}
	if err := tx.Where("id = ?", friendRequest.ReceiverID).First(&receiver).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Receiver not found"})
	}

	sender.Friends = append(sender.Friends, receiver.ID)
	receiver.Friends = append(receiver.Friends, sender.ID)

	if err := tx.Save(&sender).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	if err := tx.Save(&receiver).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Create a new chat for the friends
	chat := models.Chat{
		Members: models.UUIDArray{sender.ID, receiver.ID},
	}
	if err := tx.Create(&chat).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Create notification for sender
	notification := models.Notification{
		UserID:     friendRequest.SenderID,
		Type:       "friend_accept",
		FromUserID: userID,
		Message:    receiver.Username + " accepted your friend request",
		Read:       false,
	}
	tx.Create(&notification)

	if err := tx.Commit().Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Clear user caches
	h.redisClient.Del(context.Background(), "user:"+friendRequest.SenderID)
	h.redisClient.Del(context.Background(), "user:"+friendRequest.ReceiverID)

	// Publish notification via Redis for WebSocket
	notificationJSON, _ := json.Marshal(notification)
	h.redisClient.Publish(context.Background(), "notification:"+friendRequest.SenderID, notificationJSON)

	return c.JSON(fiber.Map{"message": "Friend request confirmed", "chatId": chat.ID})
}

// RejectFriendRequest rejects a friend request
func (h *FriendRequestHandler) RejectFriendRequest(c *fiber.Ctx) error {
	requestID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var friendRequest models.FriendRequest
	if err := h.db.Where("id = ? AND receiver_id = ?", requestID, userID).First(&friendRequest).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Friend request not found"})
	}

	if friendRequest.Status != "pending" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Friend request already processed"})
	}

	friendRequest.Status = "rejected"
	if err := h.db.Save(&friendRequest).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Publish notification
	notificationJSON, _ := json.Marshal(friendRequest)
	h.redisClient.Publish(context.Background(), "friend_request:"+friendRequest.SenderID, notificationJSON)

	return c.JSON(fiber.Map{"message": "Friend request rejected"})
}

// Setup configures the friend request routes
func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewFriendRequestHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	friend := api.Group("/friend")
	friend.Post("/request", auth.JWTMiddleware(cfg), handler.SendFriendRequest)
	friend.Get("/requests", auth.JWTMiddleware(cfg), handler.ListFriendRequests)
	friend.Put("/request/:id/confirm", auth.JWTMiddleware(cfg), handler.ConfirmFriendRequest)
	friend.Put("/request/:id/reject", auth.JWTMiddleware(cfg), handler.RejectFriendRequest)
}