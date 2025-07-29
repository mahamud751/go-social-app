package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"social-media-app/api/models"
	"social-media-app/config"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type RegisterRequest struct {
	Username       string `json:"username" validate:"required"`
	Email          string `json:"email" validate:"required,email"`
	Password       string `json:"password" validate:"required,min=6"`
	Firstname      string `json:"firstname" validate:"required"`
	Lastname       string `json:"lastname" validate:"required"`
	IsAdmin        bool   `json:"isAdmin"`
	ProfilePicture string `json:"profilePicture"`
	CoverPicture   string `json:"coverPicture"`
	About          string `json:"about"`
	LivesIn        string `json:"livesin"`
	WorksAt        string `json:"worksAt"`
	Relationship   string `json:"relationship"`
	Country        string `json:"country"`
}

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=6"`
}

type SupabaseUserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type AuthHandler struct {
	db          *gorm.DB
	redisClient *redis.Client
	cfg         *config.Config
}

func NewAuthHandler(db *gorm.DB, redisClient *redis.Client, cfg *config.Config) *AuthHandler {
	return &AuthHandler{db, redisClient, cfg}
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	// Check if user exists locally
	var existingUser models.User
	if err := h.db.Where("username = ? OR email = ?", req.Username, req.Email).First(&existingUser).Error; err == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Username or email already registered"})
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to hash password"})
	}

	// Create Supabase user
	supabaseReq := map[string]string{
		"email":    req.Email,
		"password": req.Password,
	}
	body, _ := json.Marshal(supabaseReq)
	httpReq, _ := http.NewRequest("POST", h.cfg.SupabaseURL+"/auth/v1/signup", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", h.cfg.SupabaseAnonKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to connect to Supabase"})
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		var supabaseErr map[string]interface{}
		json.Unmarshal(bodyBytes, &supabaseErr)
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"message": "Supabase error during signup",
			"error":   supabaseErr,
		})
	}

	var supabaseUser SupabaseUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&supabaseUser); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to parse Supabase response"})
	}

	user := models.User{
		ID:             supabaseUser.ID,
		Username:       req.Username,
		Email:          req.Email,
		Password:       string(hashedPassword),
		Firstname:      req.Firstname,
		Lastname:       req.Lastname,
		IsAdmin:        req.IsAdmin,
		ProfilePicture: req.ProfilePicture,
		CoverPicture:   req.CoverPicture,
		About:          req.About,
		LivesIn:        req.LivesIn,
		WorksAt:        req.WorksAt,
		Relationship:   req.Relationship,
		Country:        req.Country,
	}

	if err := h.db.Create(&user).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to save user to database"})
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": user.Username,
		"id":       user.ID,
	})
	tokenString, err := token.SignedString([]byte(h.cfg.JWTSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to generate token"})
	}

	return c.JSON(fiber.Map{"user": user, "token": tokenString})
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Invalid request"})
	}

	var user models.User
	if err := h.db.Where("email = ?", req.Email).First(&user).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "User not found"})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "Incorrect password"})
	}

	// Supabase login
	supabaseReq := map[string]string{
		"email":    req.Email,
		"password": req.Password,
	}
	body, _ := json.Marshal(supabaseReq)
	httpReq, _ := http.NewRequest("POST", h.cfg.SupabaseURL+"/auth/v1/token?grant_type=password", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("apikey", h.cfg.SupabaseAnonKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Failed to authenticate with Supabase"})
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		var supabaseErr map[string]interface{}
		json.Unmarshal(bodyBytes, &supabaseErr)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"message": "Supabase login failed",
			"error":   supabaseErr,
		})
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": user.Username,
		"id":       user.ID,
	})
	tokenString, err := token.SignedString([]byte(h.cfg.JWTSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Failed to generate token"})
	}

	return c.JSON(fiber.Map{"user": user, "token": tokenString})
}

func Setup(api fiber.Router, db *gorm.DB, redisClient *redis.Client, cfg *config.Config) {
	handler := NewAuthHandler(db, redisClient, cfg)
	auth := api.Group("/auth")
	auth.Post("/register", handler.Register)
	auth.Post("/login", handler.Login)
}
