package ws

import (
	"log"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	rtctokenbuilder "github.com/AgoraIO-Community/go-tokenbuilder/rtctokenbuilder"
)

// Add these constants and variables
const (
	agoraAppID      = "0ad1df7f5f9241e7bdccc8324d516f27"
	agoraAppCert    = "de7b71e27cbe4a1fad5783aa0a461576"
	tokenExpiryTime = 3600 // Token expiry time in seconds
)

type CallSignal struct {
	Type      string      `json:"type"`
	Channel   string      `json:"channel"`
	Data      interface{} `json:"data"`
	UserId    string      `json:"userId"`
	TargetId  string      `json:"targetId,omitempty"`
}

type User struct {
	UserID string
	Conn   *websocket.Conn
}

var (
	activeUsers = make(map[string]*User)
	mutex       sync.Mutex
	activeCalls = make(map[string]string) // channel -> initiator user ID
)

func Setup(app fiber.Router) {
	app.Get("/ws", websocket.New(handleWebSocket, websocket.Config{
		EnableCompression: true,
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
	}))
	
	// Add route for generating Agora tokens
	app.Get("/agora-token/:channel/:role/:uid", getAgoraToken)
}

func getAgoraToken(c *fiber.Ctx) error {
    channelName := c.Params("channel")
    role := c.Params("role")
    uid := c.Params("uid")

    var roleValue rtctokenbuilder.Role
    switch role {
    case "publisher":
        roleValue = rtctokenbuilder.RolePublisher
    case "subscriber":
        roleValue = rtctokenbuilder.RoleSubscriber
    default:
        return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
            "error": "Invalid role. Use 'publisher' or 'subscriber'",
        })
    }

    // Generate token with user account (string UID)
    expireTime := uint32(time.Now().Unix()) + tokenExpiryTime
    token, err := rtctokenbuilder.BuildTokenWithAccount(agoraAppID, agoraAppCert, channelName, uid, roleValue, expireTime)
    if err != nil {
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
            "error": "Failed to generate token: " + err.Error(),
        })
    }

    return c.JSON(fiber.Map{
        "token": token,
        "appId": agoraAppID,
    })
}

func handleWebSocket(c *websocket.Conn) {
	c.SetReadDeadline(time.Now().Add(60 * time.Second))
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Println("Ping error:", err)
				return
			}
			c.SetReadDeadline(time.Now().Add(60 * time.Second))
		}
	}()

	c.SetPongHandler(func(string) error {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	var userId string

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
				"data": msg.Data,
			})

		case "post-created":
			followers, ok := msg.Data["followers"].([]string)
			if !ok {
				log.Println("Invalid followers for post-created")
				continue
			}
			for _, followerId := range followers {
				sendToUser(followerId, map[string]interface{}{
					"type": "new-post",
					"data": msg.Data["post"],
				})
			}

		case "post-reaction":
			postOwner, ok := msg.Data["postOwner"].(string)
			if !ok {
				log.Println("Invalid postOwner for post-reaction")
				continue
			}
			sendToUser(postOwner, map[string]interface{}{
				"type": "post-reaction-update",
				"data": msg.Data,
			})

		case "comment-added":
			postOwner, ok := msg.Data["postOwner"].(string)
			if !ok {
				log.Println("Invalid postOwner for comment-added")
				continue
			}
			sendToUser(postOwner, map[string]interface{}{
				"type": "new-comment",
				"data": msg.Data["comment"],
			})
			parentOwner, ok := msg.Data["parentOwner"].(string)
			if ok && parentOwner != "" {
				sendToUser(parentOwner, map[string]interface{}{
					"type": "new-reply",
					"data": msg.Data["comment"],
				})
			}

		case "comment-reaction":
			commentOwner, ok := msg.Data["commentOwner"].(string)
			if !ok {
				log.Println("Invalid commentOwner for comment-reaction")
				continue
			}
			sendToUser(commentOwner, map[string]interface{}{
				"type": "comment-reaction-update",
				"data": msg.Data,
			})

		case "story-created":
			followers, ok := msg.Data["followers"].([]string)
			if !ok {
				log.Println("Invalid followers for story-created")
				continue
			}
			for _, followerId := range followers {
				sendToUser(followerId, map[string]interface{}{
					"type": "new-story",
					"data": msg.Data["story"],
				})
			}

		// Replace all call-related cases with Agora signaling
		case "agora-signal":
			// Convert the structured message to a map for the handler
			signalData := map[string]interface{}{
				"action":   msg.Data["action"],
				"targetId": msg.Data["targetId"],
				"channel":  msg.Data["channel"],
				"data":     msg.Data["data"],
			}
			handleAgoraSignal(signalData, msg.UserId, c)
		}
	}

	// Clean up user on disconnect
	mutex.Lock()
	delete(activeUsers, userId)
	// Remove user from active calls if they were in one
	for channel, initiator := range activeCalls {
		if initiator == userId {
			delete(activeCalls, channel)
			// Notify other participants
			broadcastToChannel(channel, CallSignal{
				Type: "user-left",
				Data: map[string]interface{}{
					"userId": userId,
				},
			}, userId)
		}
	}
	mutex.Unlock()
	
	log.Println("User disconnected:", userId)
	broadcastActiveUsers()
	c.Close()
}

func handleAgoraSignal(data map[string]interface{}, senderId string, conn *websocket.Conn) {
	action, ok := data["action"].(string)
	if !ok {
		log.Println("Missing action in Agora signal")
		return
	}
	
	targetId, ok := data["targetId"].(string)
	if !ok {
		log.Println("Missing targetId in Agora signal")
		return
	}
	
	// Forward signaling messages to the target user
	signal := map[string]interface{}{
		"type": "agora-signal",
		"data": data,
	}
	
	sendToUser(targetId, signal)
	
	// Handle call initiation to track active calls
	if action == "call-request" {
		channel, ok := data["channel"].(string)
		if ok {
			mutex.Lock()
			activeCalls[channel] = senderId
			mutex.Unlock()
		}
	} else if action == "call-ended" || action == "call-rejected" {
		channel, ok := data["channel"].(string)
		if ok {
			mutex.Lock()
			delete(activeCalls, channel)
			mutex.Unlock()
		}
	}
}

// Add the missing broadcastToChannel function
func broadcastToChannel(channel string, signal CallSignal, excludeUserId string) {
	mutex.Lock()
	defer mutex.Unlock()

	for _, user := range activeUsers {
		// Skip the excluded user
		if user.UserID == excludeUserId {
			continue
		}
		
		// Send the signal to all users in the channel
		if err := user.Conn.WriteJSON(map[string]interface{}{
			"type":   "channel-signal",
			"channel": channel,
			"data":   signal.Data,
			"userId": signal.UserId,
		}); err != nil {
			log.Println("Error broadcasting to channel:", channel, "user:", user.UserID, err)
		}
	}
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

// Keep all the existing Send functions (SendNotification, SendPostCreated, etc.)
func SendNotification(userId string, notification map[string]interface{}) {
	sendToUser(userId, map[string]interface{}{
		"type": "notification",
		"data": notification,
	})
}

func SendPostCreated(followers []string, post map[string]interface{}) {
	for _, followerId := range followers {
		sendToUser(followerId, map[string]interface{}{
			"type": "new-post",
			"data": post,
		})
	}
}

func SendPostReaction(postOwner string, reactionData map[string]interface{}) {
	sendToUser(postOwner, map[string]interface{}{
		"type": "post-reaction-update",
		"data": reactionData,
	})
}

func SendCommentAdded(postOwner string, parentOwner string, comment map[string]interface{}) {
	sendToUser(postOwner, map[string]interface{}{
		"type": "new-comment",
		"data": comment,
	})
	if parentOwner != "" {
		sendToUser(parentOwner, map[string]interface{}{
			"type": "new-reply",
			"data": comment,
		})
	}
}

func SendCommentReaction(commentOwner string, reactionData map[string]interface{}) {
	sendToUser(commentOwner, map[string]interface{}{
		"type": "comment-reaction-update",
		"data": reactionData,
	})
}

func SendStoryCreated(followers []string, story map[string]interface{}) {
	for _, followerId := range followers {
		sendToUser(followerId, map[string]interface{}{
			"type": "new-story",
			"data": story,
		})
	}
}