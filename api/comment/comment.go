package comment

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

type CreateCommentRequest struct {
	PostID   string `json:"postId" validate:"required"`
	UserID   string `json:"userId" validate:"required"`
	Text     string `json:"text" validate:"required"`
	ParentID string `json:"parentId"` // Optional, for replies
}

type UpdateCommentRequest struct {
	Text string `json:"text" validate:"required"`
}

type CommentHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewCommentHandler(db *gorm.DB, redisClient *redis.Client) *CommentHandler {
	return &CommentHandler{db, redisClient}
}

// CreateComment handles creating a new comment or reply
func (h *CommentHandler) CreateComment(c *fiber.Ctx) error {
	var req CreateCommentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	// Validate UUIDs
	if _, err := uuid.Parse(req.PostID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid postId format"})
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid userId format"})
	}
	if req.ParentID != "" {
		if _, err := uuid.Parse(req.ParentID); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid parentId format"})
		}
	}

	// Verify post exists
	var post models.Post
	if err := h.db.Where("id = ?", req.PostID).First(&post).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Post not found"})
	}

	// Verify user exists
	var user models.User
	if err := h.db.Where("id = ?", req.UserID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	// Verify parent comment exists if parentId is provided
	if req.ParentID != "" {
		var parentComment models.Comment
		if err := h.db.Where("id = ? AND post_id = ?", req.ParentID, req.PostID).First(&parentComment).Error; err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Parent comment not found"})
		}
	}

	// Ensure the user making the request is the same as the userId in the request
	if req.UserID != c.Locals("user_id").(string) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied: You can only create comments as yourself"})
	}

	// Convert req.ParentID (string) to *string
	var parentID *string
	if req.ParentID != "" {
		parentID = &req.ParentID
	}

	comment := models.Comment{
		PostID:   req.PostID,
		UserID:   req.UserID,
		Text:     req.Text,
		ParentID: parentID,
	}

	if err := h.db.Create(&comment).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Create notification for post owner
	var commenter models.User
	var notifications []models.Notification
	if err := h.db.Where("id = ?", req.UserID).First(&commenter).Error; err == nil {
		if post.UserID != req.UserID { // Don't notify self
			notification := models.Notification{
				UserID:     post.UserID,
				Type:       "comment",
				FromUserID: req.UserID,
				PostID:     &req.PostID,
				CommentID:  &comment.ID,
				Message:    commenter.Username + " commented on your post",
				Read:       false,
			}
			h.db.Create(&notification)
			notifications = append(notifications, notification)
		}
	}

	// If it's a reply, notify parent comment owner
	if req.ParentID != "" {
		var parentComment models.Comment
		if err := h.db.Where("id = ?", req.ParentID).First(&parentComment).Error; err == nil {
			if parentComment.UserID != req.UserID && parentComment.UserID != post.UserID { // Don't notify self or duplicate
				notification := models.Notification{
					UserID:     parentComment.UserID,
					Type:       "comment_reply",
					FromUserID: req.UserID,
					PostID:     &req.PostID,
					CommentID:  &comment.ID,
					Message:    commenter.Username + " replied to your comment",
					Read:       false,
				}
				h.db.Create(&notification)
				notifications = append(notifications, notification)
			}
		}
	}

	// Publish notifications via Redis for WebSocket
	for _, notification := range notifications {
		notificationJSON, _ := json.Marshal(notification)
		h.redisClient.Publish(context.Background(), "notification:"+notification.UserID, notificationJSON)
	}

	// Cache the comment
	commentJSON, _ := json.Marshal(comment)
	h.redisClient.Set(context.Background(), "comment:"+comment.ID, commentJSON, 3600)

	// Invalidate post comments cache
	h.redisClient.Del(context.Background(), "comments:post:"+req.PostID)

	return c.JSON(comment)
}

// GetComments retrieves all comments for a post, including replies
func (h *CommentHandler) GetComments(c *fiber.Ctx) error {
	postID := c.Params("postId")
	if _, err := uuid.Parse(postID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid postId format"})
	}

	// Check Redis cache first
	cached, err := h.redisClient.Get(context.Background(), "comments:post:"+postID).Result()
	if err == nil {
		var comments []models.Comment
		json.Unmarshal([]byte(cached), &comments)
		return c.JSON(comments)
	}

	// Fetch from database
	var comments []models.Comment
	if err := h.db.Where("post_id = ?", postID).Find(&comments).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Cache the result
	commentsJSON, _ := json.Marshal(comments)
	h.redisClient.Set(context.Background(), "comments:post:"+postID, commentsJSON, 3600)

	return c.JSON(comments)
}

// UpdateComment allows users to edit their own comments
func (h *CommentHandler) UpdateComment(c *fiber.Ctx) error {
	commentID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var req UpdateCommentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	var comment models.Comment
	if err := h.db.Where("id = ?", commentID).First(&comment).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Comment not found"})
	}

	if comment.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied: You can only edit your own comments"})
	}

	comment.Text = req.Text
	if err := h.db.Save(&comment).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Update cache
	commentJSON, _ := json.Marshal(comment)
	h.redisClient.Set(context.Background(), "comment:"+commentID, commentJSON, 3600)
	// Invalidate post comments cache
	h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)

	return c.JSON(comment)
}

// DeleteComment allows users to delete their own comments
func (h *CommentHandler) DeleteComment(c *fiber.Ctx) error {
	commentID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var comment models.Comment
	if err := h.db.Where("id = ?", commentID).First(&comment).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Comment not found"})
	}

	if comment.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied: You can only delete your own comments"})
	}

	// Delete the comment and its replies (if any)
	if err := h.db.Where("id = ? OR parent_id = ?", commentID, commentID).Delete(&models.Comment{}).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Clear caches
	h.redisClient.Del(context.Background(), "comment:"+commentID)
	h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)

	return c.JSON(fiber.Map{"message": "Comment deleted successfully"})
}

// LikeComment allows users to like or unlike a comment
func (h *CommentHandler) LikeComment(c *fiber.Ctx) error {
	commentID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var comment models.Comment
	if err := h.db.Where("id = ?", commentID).First(&comment).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Comment not found"})
	}

	// Initialize Likes if nil
	if comment.Likes == nil {
		comment.Likes = []string{}
	}

	// Check if user already liked the comment
	for i, liker := range comment.Likes {
		if liker == userID {
			// Unlike: Remove userID from likes
			comment.Likes = append(comment.Likes[:i], comment.Likes[i+1:]...)
			if err := h.db.Save(&comment).Error; err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
			}
			// Update cache
			commentJSON, _ := json.Marshal(comment)
			h.redisClient.Set(context.Background(), "comment:"+commentID, commentJSON, 3600)
			h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)
			return c.JSON(fiber.Map{"message": "Comment unliked"})
		}
	}

	// Like: Add userID to likes
	comment.Likes = append(comment.Likes, userID)
	if err := h.db.Save(&comment).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	// Create notification for comment owner
	var liker models.User
	var notification models.Notification
	if err := h.db.Where("id = ?", userID).First(&liker).Error; err == nil {
		if comment.UserID != userID { // Don't notify self
			notification = models.Notification{
				UserID:     comment.UserID,
				Type:       "comment_like",
				FromUserID: userID,
				PostID:     &comment.PostID,
				CommentID:  &comment.ID,
				Message:    liker.Username + " liked your comment",
				Read:       false,
			}
			h.db.Create(&notification)
		}
	}

	// Publish notification via Redis for WebSocket
	notificationJSON, _ := json.Marshal(notification)
	h.redisClient.Publish(context.Background(), "notification:"+comment.UserID, notificationJSON)

	// Update cache
	commentJSON, _ := json.Marshal(comment)
	h.redisClient.Set(context.Background(), "comment:"+commentID, commentJSON, 3600)
	h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)

	return c.JSON(fiber.Map{"message": "Comment liked"})
}

// Setup configures the comment routes
func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewCommentHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	comment := api.Group("/comment")
	comment.Post("/", auth.JWTMiddleware(cfg), handler.CreateComment)
	comment.Get("/post/:postId", handler.GetComments)
	comment.Put("/:id", auth.JWTMiddleware(cfg), handler.UpdateComment)
	comment.Delete("/:id", auth.JWTMiddleware(cfg), handler.DeleteComment)
	comment.Post("/:id/like", auth.JWTMiddleware(cfg), handler.LikeComment)
}