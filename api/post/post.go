package post

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

type CreatePostRequest struct {
	UserID string `json:"userId" validate:"required"`
	Desc   string `json:"desc"`
	Image  string `json:"image"`
}

type UpdatePostRequest struct {
	Desc  string `json:"desc"`
	Image string `json:"image"`
}

type PostHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewPostHandler(db *gorm.DB, redisClient *redis.Client) *PostHandler {
	return &PostHandler{db, redisClient}
}

func (h *PostHandler) CreatePost(c *fiber.Ctx) error {
	var req CreatePostRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	post := models.Post{
		UserID: req.UserID,
		Desc:   req.Desc,
		Image:  req.Image,
	}
	if err := h.db.Create(&post).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	postJSON, _ := json.Marshal(post)
	h.redisClient.Set(context.Background(), "post:"+post.ID, postJSON, 3600)
	return c.JSON(post)
}

func (h *PostHandler) GetPost(c *fiber.Ctx) error {
	postID := c.Params("id")
	cached, err := h.redisClient.Get(context.Background(), "post:"+postID).Result()
	if err == nil {
		var post models.Post
		json.Unmarshal([]byte(cached), &post)
		return c.JSON(post)
	}

	var post models.Post
	if err := h.db.Where("id = ?", postID).First(&post).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Post not found"})
	}

	postJSON, _ := json.Marshal(post)
	h.redisClient.Set(context.Background(), "post:"+postID, postJSON, 3600)
	return c.JSON(post)
}

func (h *PostHandler) UpdatePost(c *fiber.Ctx) error {
	postID := c.Params("id")
	var req UpdatePostRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	var post models.Post
	if err := h.db.Where("id = ?", postID).First(&post).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Post not found"})
	}

	if post.UserID != c.Locals("user_id").(string) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied"})
	}

	if req.Desc != "" {
		post.Desc = req.Desc
	}
	if req.Image != "" {
		post.Image = req.Image
	}

	if err := h.db.Save(&post).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	postJSON, _ := json.Marshal(post)
	h.redisClient.Set(context.Background(), "post:"+postID, postJSON, 3600)
	return c.JSON(post)
}

func (h *PostHandler) DeletePost(c *fiber.Ctx) error {
	postID := c.Params("id")
	var post models.Post
	if err := h.db.Where("id = ?", postID).First(&post).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Post not found"})
	}

	if post.UserID != c.Locals("user_id").(string) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied"})
	}

	if err := h.db.Delete(&post).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	h.redisClient.Del(context.Background(), "post:"+postID)
	return c.JSON(fiber.Map{"message": "Post deleted"})
}

func (h *PostHandler) LikePost(c *fiber.Ctx) error {
	postID := c.Params("id")
	userID := c.Locals("user_id").(string)

	var post models.Post
	if err := h.db.Where("id = ?", postID).First(&post).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Post not found"})
	}

	for i, liker := range post.Likes {
		if liker == userID {
			post.Likes = append(post.Likes[:i], post.Likes[i+1:]...)
			if err := h.db.Save(&post).Error; err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
			}
			postJSON, _ := json.Marshal(post)
			h.redisClient.Set(context.Background(), "post:"+postID, postJSON, 3600)
			return c.JSON(fiber.Map{"message": "Post unliked"})
		}
	}

	post.Likes = append(post.Likes, userID)
	if err := h.db.Save(&post).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	postJSON, _ := json.Marshal(post)
	h.redisClient.Set(context.Background(), "post:"+postID, postJSON, 3600)
	return c.JSON(fiber.Map{"message": "Post liked"})
}

func (h *PostHandler) GetTimelinePosts(c *fiber.Ctx) error {
	userID := c.Params("id")
	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	var posts []models.Post
	if err := h.db.Where("user_id = ? OR user_id IN ?", userID, user.Following).Find(&posts).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	return c.JSON(posts)
}

func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewPostHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	post := api.Group("/post")
	post.Post("/", auth.JWTMiddleware(cfg), handler.CreatePost)
	post.Get("/:id", handler.GetPost)
	post.Put("/:id", auth.JWTMiddleware(cfg), handler.UpdatePost)
	post.Delete("/:id", auth.JWTMiddleware(cfg), handler.DeletePost)
	post.Put("/:id/like", auth.JWTMiddleware(cfg), handler.LikePost)
	post.Get("/:id/timeline", handler.GetTimelinePosts)
}