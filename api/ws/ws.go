package ws

import (
	"log"
	"sync"
	"time"

	rtctokenbuilder "github.com/AgoraIO/Tools/DynamicKey/AgoraDynamicKey/go/src/rtctokenbuilder2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
)

// Constants and variables
const (
	agoraAppID      = "0ad1df7f5f9241e7bdccc8324d516f27"
	agoraAppCert    = "de7b71e27cbe4a1fad5783aa0a461576"
	tokenExpiryTime = 3600 // Token expiry time in seconds
)

type CallSignal struct {
	Type     string      `json:"type"`
	Channel  string      `json:"channel"`
	Data     interface{} `json:"data"`
	UserId   string      `json:"userId"`
	TargetId string      `json:"targetId,omitempty"`
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
	// WebSocket endpoint
	app.Get("/", websocket.New(handleWebSocket, websocket.Config{
		EnableCompression: true,
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
	}))
}

func GetAgoraToken(c *fiber.Ctx) error {
	channel := c.Query("channel")
	role := c.Query("role")
	uid := c.Query("uid")

	if channel == "" || role == "" || uid == "" {
		log.Printf("Missing parameters: channel=%s, role=%s, uid=%s", channel, role, uid)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Missing required parameters: channel, role, uid",
		})
	}

	log.Printf("Generating token for channel: %s, role: %s, uid: %s", channel, role, uid)

	return GenerateAgoraToken(c, channel, role, uid)
}

func GenerateAgoraToken(c *fiber.Ctx, channel, role, uid string) error {
	var roleValue rtctokenbuilder.Role
	switch role {
	case "publisher":
		roleValue = rtctokenbuilder.RolePublisher
	case "subscriber":
		roleValue = rtctokenbuilder.RoleSubscriber
	default:
		log.Printf("Invalid role: %s", role)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid role. Use 'publisher' or 'subscriber'",
		})
	}

	expireTime := uint32(time.Now().Unix() + tokenExpiryTime)
	token, err := rtctokenbuilder.BuildTokenWithUserAccount(
		agoraAppID,
		agoraAppCert,
		channel,
		uid,
		roleValue,
		expireTime,
		expireTime,
	)
	if err != nil {
		log.Printf("Failed to generate token with UserAccount: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate token: " + err.Error(),
		})
	}

	log.Printf("Generated token successfully for channel: %s, uid: %s", channel, uid)
	return c.JSON(fiber.Map{
		"token":   token,
		"appId":   agoraAppID,
		"channel": channel,
		"uid":     uid,
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
				log.Printf("WebSocket closed normally for user %s: %v", userId, err)
			} else {
				log.Printf("WebSocket read error for user %s: %v", userId, err)
			}
			break
		}

		log.Printf("Received message from user %s: type=%s", msg.UserId, msg.Type)

		switch msg.Type {
		case "new-user-add":
			userId = msg.UserId
			mutex.Lock()
			if _, exists := activeUsers[userId]; exists {
				log.Printf("User already connected: %s", userId)
				mutex.Unlock()
				continue
			}
			activeUsers[userId] = &User{UserID: userId, Conn: c}
			mutex.Unlock()
			log.Printf("User connected: %s", userId)
			broadcastActiveUsers()

		case "send-message":
			receiverId, ok := msg.Data["receiverId"].(string)
			if !ok {
				log.Println("Invalid receiverId in send-message")
				continue
			}
			log.Printf("Sending message from %s to %s", msg.UserId, receiverId)
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
			log.Printf("Sending notification to %s", receiverId)
			sendToUser(receiverId, map[string]interface{}{
				"type": "notification",
				"data": msg.Data,
			})

		case "post-created":
			followers, ok := msg.Data["followers"].([]interface{})
			if !ok {
				log.Println("Invalid followers for post-created")
				continue
			}
			followerIds := make([]string, len(followers))
			for i, f := range followers {
				followerIds[i], _ = f.(string)
			}
			log.Printf("Broadcasting post-created to %d followers", len(followerIds))
			for _, followerId := range followerIds {
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
			log.Printf("Sending post-reaction to %s", postOwner)
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
			log.Printf("Sending comment-added to %s", postOwner)
			sendToUser(postOwner, map[string]interface{}{
				"type": "new-comment",
				"data": msg.Data["comment"],
			})
			parentOwner, ok := msg.Data["parentOwner"].(string)
			if ok && parentOwner != "" {
				log.Printf("Sending reply notification to %s", parentOwner)
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
			log.Printf("Sending comment-reaction to %s", commentOwner)
			sendToUser(commentOwner, map[string]interface{}{
				"type": "comment-reaction-update",
				"data": msg.Data,
			})

		case "story-created":
			followers, ok := msg.Data["followers"].([]interface{})
			if !ok {
				log.Println("Invalid followers for story-created")
				continue
			}
			followerIds := make([]string, len(followers))
			for i, f := range followers {
				followerIds[i], _ = f.(string)
			}
			log.Printf("Broadcasting story-created to %d followers", len(followerIds))
			for _, followerId := range followerIds {
				sendToUser(followerId, map[string]interface{}{
					"type": "new-story",
					"data": msg.Data["story"],
				})
			}
		case "agora-signal":
			action, ok := msg.Data["action"].(string)
			if !ok {
				log.Println("Invalid action in agora-signal")
				continue
			}

			targetId, ok := msg.Data["targetId"].(string)
			if !ok || targetId == "" {
				log.Println("No targetId provided in agora-signal")
				continue
			}

			// For call requests, generate a token for the receiver too
			if action == "call-request" {
				channel, ok := msg.Data["channel"].(string)
				if ok {
					// Generate token for receiver
					token, err := generateTokenForUser(channel, "publisher", targetId)
					if err == nil {
						msg.Data["receiverToken"] = token
						msg.Data["appId"] = agoraAppID
					}
				}
			}

			// Forward the signal to the target user
			sendToUser(targetId, map[string]interface{}{
				"type":   "agora-signal",
				"userId": msg.UserId,
				"data":   msg.Data,
			})
		}
	}

	// Clean up user on disconnect
	mutex.Lock()
	delete(activeUsers, userId)
	for channel, initiator := range activeCalls {
		if initiator == userId {
			delete(activeCalls, channel)
			log.Printf("Removed active call on disconnect: channel %s", channel)
			// Notify other participant
			for _, user := range activeUsers {
				if user.UserID != userId {
					sendToUser(user.UserID, map[string]interface{}{
						"type":   "agora-signal",
						"userId": userId,
						"data": map[string]interface{}{
							"action":   "call-ended",
							"channel":  channel,
							"targetId": user.UserID,
						},
					})
				}
			}
		}
	}
	mutex.Unlock()

	log.Printf("User disconnected: %s", userId)
	broadcastActiveUsers()
	c.Close()
}
func generateTokenForUser(channel, role, uid string) (string, error) {
	var roleValue rtctokenbuilder.Role
	if role == "publisher" {
		roleValue = rtctokenbuilder.RolePublisher
	} else {
		roleValue = rtctokenbuilder.RoleSubscriber
	}

	expireTime := uint32(time.Now().Unix() + tokenExpiryTime)
	return rtctokenbuilder.BuildTokenWithUserAccount(
		agoraAppID,
		agoraAppCert,
		channel,
		uid,
		roleValue,
		expireTime,
		expireTime,
	)
}

func broadcastActiveUsers() {
	userIds := []string{}
	mutex.Lock()
	for id := range activeUsers {
		userIds = append(userIds, id)
	}
	mutex.Unlock()

	log.Printf("Broadcasting active users: %v", userIds)
	for _, user := range activeUsers {
		if err := user.Conn.WriteJSON(map[string]interface{}{
			"type": "get-users",
			"data": userIds,
		}); err != nil {
			log.Printf("Error broadcasting to user %s: %v", user.UserID, err)
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
			log.Printf("Error sending to user %s: %v", userId, err)
			user.Conn.Close()
			delete(activeUsers, userId)
		} else {
			log.Printf("Sent message to user %s", userId)
		}
	} else {
		log.Printf("User %s not found in active users", userId)
	}
}

// Existing Send functions (unchanged)
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
