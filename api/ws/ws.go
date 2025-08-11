package ws

import (
	"log"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
)

type User struct {
	UserID string
	Conn   *websocket.Conn
}

var (
	activeUsers = make(map[string]*User)
	mutex       sync.Mutex
)

func Setup(app fiber.Router) {
	app.Get("/ws", websocket.New(handleWebSocket, websocket.Config{
		// Enable compression for better performance
		EnableCompression: true,
		// Set read/write timeouts
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}))
}

func handleWebSocket(c *websocket.Conn) {
	// Set up ping/pong mechanism
	c.SetReadDeadline(time.Now().Add(60 * time.Second)) // Initial timeout
	go func() {
		ticker := time.NewTicker(30 * time.Second) // Send ping every 30 seconds
		defer ticker.Stop()
		for range ticker.C {
			if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Println("Ping error:", err)
				return
			}
			// Update read deadline on each ping
			c.SetReadDeadline(time.Now().Add(60 * time.Second))
		}
	}()

	var userId string

	// Handle pong messages
	c.SetPongHandler(func(string) error {
		// Reset read deadline on receiving pong
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Read messages
	for {
		var msg struct {
			Type   string                 `json:"type"`
			UserId string                 `json:"userId"`
			Data   map[string]interface{} `json:"data"`
		}

		if err := c.ReadJSON(&msg); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Println("WebSocket closed normally:", err)
			} else {
				log.Println("WebSocket read error:", err)
			}
			break
		}

		switch msg.Type {
		case "new-user-add":
			userId = msg.UserId
			mutex.Lock()
			if _, exists := activeUsers[userId]; exists {
				log.Println("User already connected:", userId)
				mutex.Unlock()
				continue
			}
			activeUsers[userId] = &User{UserID: userId, Conn: c}
			mutex.Unlock()
			log.Println("User connected:", userId)
			broadcastActiveUsers()

		case "send-message":
			receiverId, ok := msg.Data["receiverId"].(string)
			if !ok {
				log.Println("Invalid receiverId")
				continue
			}
			sendToUser(receiverId, map[string]interface{}{
				"type": "receive-message",
				"data": map[string]interface{}{
					"chatId":    msg.Data["chatId"],
					"senderId":  msg.Data["senderId"],
					"text":      msg.Data["text"],
					"createdAt": msg.Data["createdAt"],
				},
			})

		case "notification":
			receiverId, ok := msg.Data["receiverId"].(string)
			if !ok {
				log.Println("Invalid receiverId for notification")
				continue
			}
			sendToUser(receiverId, map[string]interface{}{
				"type": "notification",
				"data": map[string]interface{}{
					"id":           msg.Data["id"],
					"type":         msg.Data["type"],
					"fromUserId":   msg.Data["fromUserId"],
					"fromUserName": msg.Data["fromUserName"],
					"postId":       msg.Data["postId"],
					"commentId":    msg.Data["commentId"],
					"message":      msg.Data["message"],
					"read":         msg.Data["read"],
					"createdAt":    msg.Data["createdAt"],
				},
			})
		}
	}

	// Cleanup on disconnect
	mutex.Lock()
	delete(activeUsers, userId)
	mutex.Unlock()
	log.Println("User disconnected:", userId)
	broadcastActiveUsers()
	c.Close()
}

func broadcastActiveUsers() {
	userIds := []string{}
	for id := range activeUsers {
		userIds = append(userIds, id)
	}
	for _, user := range activeUsers {
		if err := user.Conn.WriteJSON(map[string]interface{}{
			"type": "get-users",
			"data": userIds,
		}); err != nil {
			log.Println("Error broadcasting to user:", user.UserID, err)
			user.Conn.Close()
			mutex.Lock()
			delete(activeUsers, user.UserID)
			mutex.Unlock()
		}
	}
}

func sendToUser(userId string, payload interface{}) {
	mutex.Lock()
	defer mutex.Unlock()

	if user, ok := activeUsers[userId]; ok {
		if err := user.Conn.WriteJSON(payload); err != nil {
			log.Println("Error sending to user:", userId, err)
			user.Conn.Close()
			delete(activeUsers, userId)
		}
	}
}

func SendNotification(userId string, notification map[string]interface{}) {
	sendToUser(userId, map[string]interface{}{
		"type": "notification",
		"data": notification,
	})
}