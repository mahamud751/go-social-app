package user

import (
	"context"
	"encoding/json"
	"social-media-app/api/auth"
	"social-media-app/api/models"
	"social-media-app/config"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

type UpdateUserRequest struct {
	ID                 string `json:"id" validate:"required"` // Changed from _id to id
	CurrentAdminStatus bool   `json:"currentAdminStatus"`
	Password           string `json:"password"`
	Firstname          string `json:"firstname"`
	Lastname           string `json:"lastname"`
	ProfilePicture     string `json:"profilePicture"`
	CoverPicture       string `json:"coverPicture"`
	About              string `json:"about"`
	LivesIn            string `json:"livesIn"`
	WorksAt            string `json:"worksAt"`
	Relationship       string `json:"relationship"`
	Country            string `json:"country"`
}

type UserHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
}

func NewUserHandler(db *gorm.DB, redisClient *redis.Client) *UserHandler {
	return &UserHandler{db, redisClient}
}

func (h *UserHandler) GetAllUsers(c *fiber.Ctx) error {
	var users []models.User
	if err := h.db.Find(&users).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	return c.JSON(users)
}

func (h *UserHandler) GetUser(c *fiber.Ctx) error {
	userID := c.Params("id")
	cached, err := h.redisClient.Get(context.Background(), "user:"+userID).Result()
	if err == nil {
		var user models.User
		json.Unmarshal([]byte(cached), &user)
		return c.JSON(user)
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	userJSON, _ := json.Marshal(user)
	h.redisClient.Set(context.Background(), "user:"+userID, userJSON, 3600)
	return c.JSON(user)
}

func (h *UserHandler) UpdateUser(c *fiber.Ctx) error {
	userID := c.Params("id")
	currentUserID := c.Locals("user_id").(string)
	var req UpdateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	if userID != req.ID || userID != currentUserID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied: You can only update your own profile"})
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	// Update fields if provided
	if req.Password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to hash password"})
		}
		user.Password = string(hashedPassword)
	}
	if req.Firstname != "" {
		user.Firstname = req.Firstname
	}
	if req.Lastname != "" {
		user.Lastname = req.Lastname
	}
	if req.ProfilePicture != "" {
		user.ProfilePicture = req.ProfilePicture
	}
	if req.CoverPicture != "" {
		user.CoverPicture = req.CoverPicture
	}
	if req.About != "" {
		user.About = req.About
	}
	if req.LivesIn != "" {
		user.LivesIn = req.LivesIn
	}
	if req.WorksAt != "" {
		user.WorksAt = req.WorksAt
	}
	if req.Relationship != "" {
		user.Relationship = req.Relationship
	}
	if req.Country != "" {
		user.Country = req.Country
	}

	if err := h.db.Save(&user).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": user.Username,
		"id":       user.ID,
	})
	cfg, err := config.LoadConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to load config"})
	}
	tokenString, err := token.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to generate token"})
	}

	userJSON, _ := json.Marshal(user)
	h.redisClient.Set(context.Background(), "user:"+userID, userJSON, 3600)
	return c.JSON(fiber.Map{"user": user, "token": tokenString})
}

func (h *UserHandler) DeleteUser(c *fiber.Ctx) error {
	userID := c.Params("id")
	currentUserID := c.Locals("user_id").(string)
	var req UpdateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	var user models.User
	if err := h.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	if userID != currentUserID && !req.CurrentAdminStatus {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Access denied: You can only delete your own profile or need admin status"})
	}

	if err := h.db.Where("id = ?", userID).Delete(&models.User{}).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	h.redisClient.Del(context.Background(), "user:"+userID)
	return c.JSON(fiber.Map{"message": "Successfully deleted"})
}

func (h *UserHandler) FollowUser(c *fiber.Ctx) error {
    followID := c.Params("id")
    currentUserID := c.Locals("user_id").(string)

    if followID == currentUserID {
        return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Cannot follow yourself"})
    }

    tx := h.db.Begin()
    defer func() {
        if r := recover(); r != nil {
            tx.Rollback()
        }
    }()

    // Check if already following
    var count int64
    err := tx.Model(&models.User{}).
        Joins("JOIN users u ON u.id = ?", currentUserID).
        Where("users.id = ? AND ? = ANY(u.following)", followID, followID).
        Count(&count).Error
    if err != nil {
        tx.Rollback()
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
    }
    if count > 0 {
        tx.Rollback()
        return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "You are already following this user"})
    }

    // Update following list for current user
    err = tx.Exec(`
        UPDATE users 
        SET following = array_append(following, ?) 
        WHERE id = ? AND NOT ? = ANY(following)`,
        followID, currentUserID, followID).Error
    if err != nil {
        tx.Rollback()
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
    }

    // Update followers list for target user
    err = tx.Exec(`
        UPDATE users 
        SET followers = array_append(followers, ?) 
        WHERE id = ? AND NOT ? = ANY(followers)`,
        currentUserID, followID, currentUserID).Error
    if err != nil {
        tx.Rollback()
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
    }

    if err := tx.Commit().Error; err != nil {
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
    }

    // Clear cache
    h.redisClient.Del(context.Background(), "user:"+followID)
    h.redisClient.Del(context.Background(), "user:"+currentUserID)

    return c.JSON(fiber.Map{"message": "User followed successfully"})
}

func (h *UserHandler) UnfollowUser(c *fiber.Ctx) error {
	unfollowID := c.Params("id")
	currentUserID := c.Locals("user_id").(string)

	var unfollowUser, currentUser models.User
	if err := h.db.Where("id = ?", unfollowID).First(&unfollowUser).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User to unfollow not found"})
	}
	if err := h.db.Where("id = ?", currentUserID).First(&currentUser).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "Current user not found"})
	}

	if unfollowID == currentUserID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "Cannot unfollow yourself"})
	}

	for i, follower := range unfollowUser.Followers {
		if follower == currentUserID {
			unfollowUser.Followers = append(unfollowUser.Followers[:i], unfollowUser.Followers[i+1:]...)
			break
		}
	}
	for i, following := range currentUser.Following {
		if following == unfollowID {
			currentUser.Following = append(currentUser.Following[:i], currentUser.Following[i+1:]...)
			break
		}
	}

	if err := h.db.Save(&unfollowUser).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	if err := h.db.Save(&currentUser).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}

	unfollowUserJSON, _ := json.Marshal(unfollowUser)
	currentUserJSON, _ := json.Marshal(currentUser)
	h.redisClient.Set(context.Background(), "user:"+unfollowID, unfollowUserJSON, 3600)
	h.redisClient.Set(context.Background(), "user:"+currentUserID, currentUserJSON, 3600)
	return c.JSON(fiber.Map{"message": "Unfollowed successfully"})
}

func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client) {
	handler := NewUserHandler(db, redisClient)
	cfg, err := config.LoadConfig()
	if err != nil {
		panic("Failed to load config: " + err.Error())
	}
	user := api.Group("/user")
	user.Get("/", handler.GetAllUsers)
	user.Get("/:id", handler.GetUser)
	user.Put("/:id", auth.JWTMiddleware(cfg), handler.UpdateUser)
	user.Delete("/:id", auth.JWTMiddleware(cfg), handler.DeleteUser)
	user.Put("/:id/follow", auth.JWTMiddleware(cfg), handler.FollowUser)
	user.Put("/:id/unfollow", auth.JWTMiddleware(cfg), handler.UnfollowUser)
}