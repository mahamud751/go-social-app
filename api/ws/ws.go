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
		EnableCompression: true,
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
	}))
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
		case "call-offer":
			receiverId, ok := msg.Data["receiverId"].(string)
			if !ok {
			  log.Println("Invalid receiverId for call-offer")
			  continue
			}
			offer, ok := msg.Data["offer"].(map[string]interface{})
			if !ok {
			  log.Println("Invalid offer for call-offer")
			  continue
			}
			callType, ok := msg.Data["callType"].(string)
			if !ok {
			  log.Println("Invalid callType for call-offer")
			  continue
			}
			senderId, ok := msg.Data["senderId"].(string)
			if !ok {
			  log.Println("Invalid senderId for call-offer")
			  continue
			}
			log.Printf("Forwarding call-offer: senderId=%s, receiverId=%s, callType=%s", senderId, receiverId, callType)
			sendToUser(receiverId, map[string]interface{}{
			  "type": "incoming-call-offer",
			  "data": map[string]interface{}{
				"callerId": senderId,
				"offer":    offer,
				"callType": callType,
			  },
			})
		  
		  case "call-answer":
			callerId, ok := msg.Data["callerId"].(string)
			if !ok {
			  log.Println("Invalid callerId for call-answer")
			  continue
			}
			answer, ok := msg.Data["answer"].(map[string]interface{})
			if !ok {
			  log.Println("Invalid answer for call-answer")
			  continue
			}
			log.Printf("Forwarding call-answer: callerId=%s", callerId)
			sendToUser(callerId, map[string]interface{}{
			  "type": "call-answer",
			  "data": map[string]interface{}{
				"answer": answer,
			  },
			})
		  
		  case "ice-candidate":
			targetId, ok := msg.Data["targetId"].(string)
			if !ok {
			  log.Println("Invalid targetId for ice-candidate")
			  continue
			}
			candidate, ok := msg.Data["candidate"].(map[string]interface{})
			if !ok {
			  log.Println("Invalid candidate for ice-candidate")
			  continue
			}
			log.Printf("Forwarding ice-candidate: targetId=%s", targetId)
			sendToUser(targetId, map[string]interface{}{
			  "type": "new-ice-candidate",
			  "data": map[string]interface{}{
				"candidate": candidate,
			  },
			})
			case "decline-call":
			callerId, ok := msg.Data["callerId"].(string)
			if !ok {
				log.Println("Invalid callerId for decline-call")
				continue
			}
			sendToUser(callerId, map[string]interface{}{
				"type": "call-declined",
				"data": nil,
			})

		case "end-call":
			peerId, ok := msg.Data["peerId"].(string)
			if !ok {
				log.Println("Invalid peerId for end-call")
				continue
			}
			sendToUser(peerId, map[string]interface{}{
				"type": "call-ended",
				"data": nil,
			})
		
		}
	}

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