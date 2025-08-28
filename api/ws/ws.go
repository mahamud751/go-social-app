package ws

import (
	"log"
	"strconv"
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
	uid := c.Query("uid")
	// For bidirectional video calls, both users should be publishers
	role := "publisher"

	if channel == "" || uid == "" {
		log.Printf("Missing parameters: channel=%s, uid=%s", channel, uid)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Missing required parameters: channel, uid",
		})
	}

	log.Printf("Generating token for channel: %s, role: %s, uid: %s", channel, role, uid)

	return GenerateAgoraToken(c, channel, role, uid)
}

// SetupCall - New endpoint for setting up bidirectional calls
func SetupCall(c *fiber.Ctx) error {
	type CallSetupRequest struct {
		Channel  string `json:"channel"`
		CallerID string `json:"callerId"`
		CalleeID string `json:"calleeId"`
	}

	var req CallSetupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Channel == "" || req.CallerID == "" || req.CalleeID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Missing required parameters: channel, callerId, calleeId",
		})
	}

	log.Printf("Setting up call: channel=%s, caller=%s, callee=%s", req.Channel, req.CallerID, req.CalleeID)

	// Generate tokens for both users as publishers
	callerToken, err := GetTokenForUser(req.CallerID, req.Channel)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate caller token: " + err.Error(),
		})
	}

	calleeToken, err := GetTokenForUser(req.CalleeID, req.Channel)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate callee token: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"channel": req.Channel,
		"tokens": map[string]interface{}{
			"caller": callerToken,
			"callee": calleeToken,
		},
		"message": "Call setup completed - both users have publisher tokens",
	})
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

	// Try BuildTokenWithUid
	expireTime := uint32(time.Now().Unix()) + tokenExpiryTime
	var token string
	var err error

	// Convert string UID to uint32 for BuildTokenWithUid
	uidInt, err := strconv.ParseUint(uid, 10, 32)
	if err == nil {
		// Use BuildTokenWithUid if available
		token, err = rtctokenbuilder.BuildTokenWithUid(agoraAppID, agoraAppCert, channel, uint32(uidInt), roleValue, expireTime, expireTime)
		if err != nil {
			log.Printf("Failed to generate token with UID: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to generate token: " + err.Error(),
			})
		}
	} else {
		// Fallback to BuildTokenWithUserAccount if UID is not numeric
		log.Printf("Invalid UID for numeric conversion: %s, falling back to BuildTokenWithUserAccount", uid)
		token, err = rtctokenbuilder.BuildTokenWithUserAccount(agoraAppID, agoraAppCert, channel, uid, roleValue, expireTime, expireTime)
		if err != nil {
			log.Printf("Failed to generate token with UserAccount: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to generate token: " + err.Error(),
			})
		}
	}

	log.Printf("Generated token successfully for channel: %s, uid: %s", channel, uid)
	return c.JSON(fiber.Map{
		"token":   token,
		"appId":   agoraAppID,
		"channel": channel,
		"uid":     uid,
	})
}

// Rest of the code remains unchanged
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
			followers, ok := msg.Data["followers"].([]string)
			if !ok {
				log.Println("Invalid followers for post-created")
				continue
			}
			log.Printf("Broadcasting post-created to %d followers", len(followers))
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
			followers, ok := msg.Data["followers"].([]string)
			if !ok {
				log.Println("Invalid followers for story-created")
				continue
			}
			log.Printf("Broadcasting story-created to %d followers", len(followers))
			for _, followerId := range followers {
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

			log.Printf("Agora signal: action=%s from %s to %s", action, msg.UserId, targetId)

			// Handle different signaling actions
			switch action {
			case "call-request":
				// Store call information
				channel, _ := msg.Data["channel"].(string)
				if channel != "" {
					mutex.Lock()
					activeCalls[channel] = msg.UserId
					mutex.Unlock()
					log.Printf("Registered call: channel=%s, initiator=%s", channel, msg.UserId)
				}

				// Forward call request with enhanced data
				sendToUser(targetId, map[string]interface{}{
					"type":   "agora-signal",
					"userId": msg.UserId,
					"data": map[string]interface{}{
						"action":   "call-request",
						"channel":  channel,
						"callerId": msg.UserId,
						"targetId": targetId,
						"callType": msg.Data["callType"], // video or audio
					},
				})

			case "call-accepted":
				// Both users need tokens as publishers for bidirectional calls
				channel, _ := msg.Data["channel"].(string)
				log.Printf("Call accepted: channel=%s, caller=%s, callee=%s", channel, targetId, msg.UserId)

				// Setup bidirectional call with tokens for both users
				if channel != "" {
					go InitiateBidirectionalCall(targetId, msg.UserId, channel)
				}

				// Forward acceptance to caller
				sendToUser(targetId, map[string]interface{}{
					"type":   "agora-signal",
					"userId": msg.UserId,
					"data": map[string]interface{}{
						"action":   "call-accepted",
						"channel":  channel,
						"calleeId": msg.UserId,
						"callerId": targetId,
					},
				})

			case "call-rejected":
				channel, _ := msg.Data["channel"].(string)
				if channel != "" {
					mutex.Lock()
					delete(activeCalls, channel)
					mutex.Unlock()
					log.Printf("Call rejected: channel=%s", channel)
				}

				sendToUser(targetId, map[string]interface{}{
					"type":   "agora-signal",
					"userId": msg.UserId,
					"data": map[string]interface{}{
						"action":   "call-rejected",
						"channel":  channel,
						"calleeId": msg.UserId,
					},
				})

			case "call-ended":
				channel, _ := msg.Data["channel"].(string)
				if channel != "" {
					mutex.Lock()
					delete(activeCalls, channel)
					mutex.Unlock()
					log.Printf("Call ended: channel=%s", channel)
				}

				sendToUser(targetId, map[string]interface{}{
					"type":   "agora-signal",
					"userId": msg.UserId,
					"data": map[string]interface{}{
						"action":  "call-ended",
						"channel": channel,
						"endedBy": msg.UserId,
					},
				})

			case "ice-candidate", "offer", "answer":
				// Forward WebRTC signaling messages for peer connection
				log.Printf("Forwarding WebRTC signal: %s from %s to %s", action, msg.UserId, targetId)
				sendToUser(targetId, map[string]interface{}{
					"type":   "agora-signal",
					"userId": msg.UserId,
					"data":   msg.Data,
				})

			default:
				// Forward any other signaling messages
				log.Printf("Forwarding signal: %s from %s to %s", action, msg.UserId, targetId)
				sendToUser(targetId, map[string]interface{}{
					"type":   "agora-signal",
					"userId": msg.UserId,
					"data":   msg.Data,
				})
			}

		}
	}

	// Clean up user on disconnect
	mutex.Lock()
	delete(activeUsers, userId)
	for channel, initiator := range activeCalls {
		if initiator == userId {
			delete(activeCalls, channel)
			log.Printf("Broadcasting user-left for channel %s", channel)
			broadcastToChannel(channel, CallSignal{
				Type: "user-left",
				Data: map[string]interface{}{
					"userId": userId,
				},
			}, userId)
		}
	}
	mutex.Unlock()

	log.Printf("User disconnected: %s", userId)
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

	log.Printf("Forwarding agora-signal: action=%s, from=%s, to=%s", action, senderId, targetId)

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
			log.Printf("Registered call: channel=%s, initiator=%s", channel, senderId)
			mutex.Unlock()
		} else {
			log.Println("Missing channel in call-request")
		}
	} else if action == "call-ended" || action == "call-rejected" {
		channel, ok := data["channel"].(string)
		if ok {
			mutex.Lock()
			delete(activeCalls, channel)
			log.Printf("Removed call: channel=%s", channel)
			mutex.Unlock()
		} else {
			log.Println("Missing channel in call-ended or call-rejected")
		}
	}
}

func broadcastToChannel(channel string, signal CallSignal, excludeUserId string) {
	mutex.Lock()
	defer mutex.Unlock()

	log.Printf("Broadcasting to channel %s, excluding user %s", channel, excludeUserId)
	for _, user := range activeUsers {
		if user.UserID == excludeUserId {
			continue
		}

		if err := user.Conn.WriteJSON(map[string]interface{}{
			"type":    "channel-signal",
			"channel": channel,
			"data":    signal.Data,
			"userId":  signal.UserId,
		}); err != nil {
			log.Printf("Error broadcasting to channel %s, user %s: %v", channel, user.UserID, err)
		}
	}
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

// Add after the sendToUser function

// GetTokenForUser generates an Agora token for a specific user and channel
func GetTokenForUser(userId, channel string) (map[string]interface{}, error) {
	// Both users should be publishers for bidirectional video/audio
	role := "publisher"

	var roleValue rtctokenbuilder.Role
	roleValue = rtctokenbuilder.RolePublisher

	expireTime := uint32(time.Now().Unix()) + tokenExpiryTime
	var token string
	var err error

	// Convert string UID to uint32 for BuildTokenWithUid
	uidInt, err := strconv.ParseUint(userId, 10, 32)
	if err == nil {
		// Use BuildTokenWithUid if available
		token, err = rtctokenbuilder.BuildTokenWithUid(agoraAppID, agoraAppCert, channel, uint32(uidInt), roleValue, expireTime, expireTime)
		if err != nil {
			log.Printf("Failed to generate token with UID for user %s: %v", userId, err)
			return nil, err
		}
	} else {
		// Fallback to BuildTokenWithUserAccount if UID is not numeric
		token, err = rtctokenbuilder.BuildTokenWithUserAccount(agoraAppID, agoraAppCert, channel, userId, roleValue, expireTime, expireTime)
		if err != nil {
			log.Printf("Failed to generate token with UserAccount for user %s: %v", userId, err)
			return nil, err
		}
	}

	log.Printf("Generated token successfully for user: %s, channel: %s", userId, channel)
	return map[string]interface{}{
		"token":   token,
		"appId":   agoraAppID,
		"channel": channel,
		"uid":     userId,
		"role":    role,
	}, nil
}

// SendTokenToUser generates and sends an Agora token to a specific user
func SendTokenToUser(userId, channel string) {
	tokenData, err := GetTokenForUser(userId, channel)
	if err != nil {
		log.Printf("Failed to generate token for user %s: %v", userId, err)
		return
	}

	sendToUser(userId, map[string]interface{}{
		"type": "agora-token",
		"data": tokenData,
	})
	log.Printf("Sent Agora token to user %s for channel %s", userId, channel)
}

// InitiateBidirectionalCall sets up tokens for both users in a call
func InitiateBidirectionalCall(callerId, calleeId, channel string) {
	log.Printf("Setting up bidirectional call: caller=%s, callee=%s, channel=%s", callerId, calleeId, channel)

	// Generate and send tokens to both users as publishers
	go SendTokenToUser(callerId, channel)
	go SendTokenToUser(calleeId, channel)
}

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
