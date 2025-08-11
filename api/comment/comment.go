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
	ParentID string `json:"parentId"`
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

func (h *CommentHandler) CreateComment(c *fiber.Ctx) error {
	var req CreateCommentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

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

	var post models.Post
	if err := h.db.Where("id = ?", req.PostID).First(&post).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Post not found"})
	}

	var user models.User
	if err := h.db.Where("id = ?", req.UserID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	if req.ParentID != "" {
		var parentComment models.Comment
		if err := h.db.Where("id = ? AND post_id = ?", req.ParentID, req.PostID).First(&parentComment).Error; err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Parent comment not found"})
		}
	}

	if req.UserID != c.Locals("user_id").(string) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied: You can only create comments as yourself"})
	}

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

	post.CommentCount++
	if err := h.db.Save(&post).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	var commenter models.User
	var notifications []models.Notification
	if err := h.db.Where("id = ?", req.UserID).First(&commenter).Error; err == nil {
		if post.UserID != req.UserID {
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

	if req.ParentID != "" {
		var parentComment models.Comment
		if err := h.db.Where("id = ?", req.ParentID).First(&parentComment).Error; err == nil {
			if parentComment.UserID != req.UserID && parentComment.UserID != post.UserID {
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

	for _, notification := range notifications {
		notificationJSON, _ := json.Marshal(notification)
		h.redisClient.Publish(context.Background(), "notification:"+notification.UserID, notificationJSON)
	}

	commentJSON, _ := json.Marshal(comment)
	h.redisClient.Set(context.Background(), "comment:"+comment.ID, commentJSON, 3600)
	h.redisClient.Del(context.Background(), "comments:post:"+req.PostID)
	h.redisClient.Del(context.Background(), "post:"+req.PostID)

	return c.JSON(comment)
}

func (h *CommentHandler) GetComments(c *fiber.Ctx) error {
	postID := c.Params("postId")
	if _, err := uuid.Parse(postID); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid postId format"})
	}

	cached, err := h.redisClient.Get(context.Background(), "comments:post:"+postID).Result()
	if err == nil {
		var comments []models.Comment
		json.Unmarshal([]byte(cached), &comments)
		return c.JSON(comments)
	}

	var comments []models.Comment
	if err := h.db.Where("post_id = ?", postID).Find(&comments).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	commentsJSON, _ := json.Marshal(comments)
	h.redisClient.Set(context.Background(), "comments:post:"+postID, commentsJSON, 3600)

	return c.JSON(comments)
}

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

	commentJSON, _ := json.Marshal(comment)
	h.redisClient.Set(context.Background(), "comment:"+commentID, commentJSON, 3600)
	h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)

	return c.JSON(comment)
}

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

	var post models.Post
	if err := h.db.Where("id = ?", comment.PostID).First(&post).Error; err == nil {
		post.CommentCount--
		if err := h.db.Save(&post).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
		}
	}

	if err := h.db.Where("id = ? OR parent_id = ?", commentID, commentID).Delete(&models.Comment{}).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	h.redisClient.Del(context.Background(), "comment:"+commentID)
	h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)
	h.redisClient.Del(context.Background(), "post:"+comment.PostID)

	return c.JSON(fiber.Map{"message": "Comment deleted successfully"})
}

func (h *CommentHandler) LikeComment(c *fiber.Ctx) error {
	commentID := c.Params("id")
	userID := c.Locals("user_id").(string)
	var req struct {
		ReactionType string `json:"reactionType"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	validReactions := map[string]bool{
		"like": true, "love": true, "haha": true, "wow": true,
		"sad": true, "angry": true, "care": true,
	}
	if req.ReactionType != "" && !validReactions[req.ReactionType] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid reaction type"})
	}

	var comment models.Comment
	if err := h.db.Where("id = ?", commentID).First(&comment).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Comment not found"})
	}

	if comment.Reactions == nil {
		comment.Reactions = make(map[string][]string)
	}

	currentReaction := ""
	for rType, users := range comment.Reactions {
		for _, id := range users {
			if id == userID {
				currentReaction = rType
				break
			}
		}
	}

	if currentReaction == req.ReactionType {
		comment.Reactions[currentReaction] = removeUser(comment.Reactions[currentReaction], userID)
		if len(comment.Reactions[currentReaction]) == 0 {
			delete(comment.Reactions, currentReaction)
		}
	} else {
		if currentReaction != "" {
			comment.Reactions[currentReaction] = removeUser(comment.Reactions[currentReaction], userID)
			if len(comment.Reactions[currentReaction]) == 0 {
				delete(comment.Reactions, currentReaction)
			}
		}
		if req.ReactionType != "" {
			comment.Reactions[req.ReactionType] = append(comment.Reactions[req.ReactionType], userID)
		}
	}

	if err := h.db.Save(&comment).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	var liker models.User
	var notification models.Notification
	if err := h.db.Where("id = ?", userID).First(&liker).Error; err == nil && req.ReactionType != "" {
		if comment.UserID != userID {
			notification = models.Notification{
				UserID:     comment.UserID,
				Type:       "comment_" + req.ReactionType,
				FromUserID: userID,
				PostID:     &comment.PostID,
				CommentID:  &comment.ID,
				Message:    liker.Username + " reacted " + req.ReactionType + " to your comment",
				Read:       false,
			}
			h.db.Create(&notification)
		}
	}

	notificationJSON, _ := json.Marshal(notification)
	h.redisClient.Publish(context.Background(), "notification:"+comment.UserID, notificationJSON)

	commentJSON, _ := json.Marshal(comment)
	h.redisClient.Set(context.Background(), "comment:"+commentID, commentJSON, 3600)
	h.redisClient.Del(context.Background(), "comments:post:"+comment.PostID)

	message := "Comment " + req.ReactionType
	if req.ReactionType == "" {
		message = "Reaction removed"
	}
	return c.JSON(fiber.Map{"message": message})
}

func removeUser(users []string, userID string) []string {
	for i, id := range users {
		if id == userID {
			return append(users[:i], users[i+1:]...)
		}
	}
	return users
}

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