package main

import (
	"log"
	"social-media-app/api/auth"
	"social-media-app/api/chat"
	"social-media-app/api/message"
	"social-media-app/api/post"
	"social-media-app/api/comment"
	"social-media-app/api/friend"
	"social-media-app/api/notification"
	"social-media-app/api/story"
	"social-media-app/api/upload"
	"social-media-app/api/user"
	"social-media-app/config"
	"social-media-app/api/ws"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	db, err := config.InitDB(cfg)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	redisClient, err := config.InitRedis(cfg)
	if err != nil {
		log.Fatal("Failed to connect to Redis:", err)
	}

	app := fiber.New()
	app.Get("/api/agora-token/:channel/:role/:uid", ws.GetAgoraToken)
	// WS group
	wsGroup := app.Group("/ws")
	ws.Setup(wsGroup)

	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	app.Static("/images", "./public/images")

	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"message": "hello users"})
	})

	api := app.Group("/api")
	auth.Setup(api, db, redisClient, cfg)
	user.Setup(api, db, redisClient)
	post.Setup(api, db, redisClient)
	chat.Setup(api, db, redisClient)
	message.Setup(api, db, redisClient)
	comment.Setup(api, db, redisClient)
	friend.Setup(api, db, redisClient)
	notification.Setup(api, db, redisClient)
	story.Setup(api, db, redisClient)
	upload.Setup(api)

	log.Fatal(app.Listen(":" + cfg.Port))
}
