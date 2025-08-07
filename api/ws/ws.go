package ws

import (
	"log"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
)

type User struct {
	UserID   string
	Conn     *websocket.Conn
}

var (
	activeUsers = make(map[string]*User) // userId => *User
	mutex       sync.Mutex
)

func Setup(app fiber.Router) {
	app.Get("/ws", websocket.New(handleWebSocket))
}

func handleWebSocket(c *websocket.Conn) {
	var userId string

	// Read initial message (like 'new-user-add')
	for {
		var msg struct {
    Type   string                 `json:"type"`
    UserId string                 `json:"userId"`
    Data   map[string]interface{} `json:"data"`
}


		if err := c.ReadJSON(&msg); err != nil {
			log.Println("WebSocket read error:", err)
			break
		}

		switch msg.Type {
		case "new-user-add":
			userId = msg.UserId
			mutex.Lock()
			activeUsers[userId] = &User{UserID: userId, Conn: c}
			mutex.Unlock()
			log.Println("User connected:", userId)
			broadcastActiveUsers()

	case "send-message":
    receiverId, _ := msg.Data["receiverId"].(string)
    sendToUser(receiverId, map[string]interface{}{
        "type": "receive-message",
        "data": map[string]interface{}{
            "chatId":    msg.Data["chatId"],
            "senderId":  msg.Data["senderId"],
            "text":      msg.Data["text"],
            "createdAt": msg.Data["createdAt"],
        },
    })

		}
	}

	// Disconnect
	mutex.Lock()
	delete(activeUsers, userId)
	mutex.Unlock()
	log.Println("User disconnected:", userId)
	broadcastActiveUsers()
}

func broadcastActiveUsers() {
	userIds := []string{}
	for id := range activeUsers {
		userIds = append(userIds, id)
	}
	for _, user := range activeUsers {
		user.Conn.WriteJSON(map[string]interface{}{
			"type": "get-users",
			"data": userIds,
		})
	}
}

func sendToUser(userId string, payload interface{}) {
	mutex.Lock()
	defer mutex.Unlock()

	if user, ok := activeUsers[userId]; ok {
		user.Conn.WriteJSON(payload)
	}
}
