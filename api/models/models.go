package models

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// StringArray is a custom type to handle PostgreSQL text arrays
type StringArray []string

func (a *StringArray) Scan(value interface{}) error {
	if value == nil {
		*a = StringArray{}
		return nil
	}
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("failed to scan array: not a string")
	}
	
	str = strings.TrimPrefix(str, "{")
	str = strings.TrimSuffix(str, "}")
	
	if str == "" {
		*a = StringArray{}
		return nil
	}
	
	*a = StringArray(strings.Split(str, ","))
	return nil
}

func (a StringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	return "{" + strings.Join(a, ",") + "}", nil
}

// UUIDArray is a custom type to handle PostgreSQL uuid arrays
type UUIDArray []string

func (a *UUIDArray) Scan(value interface{}) error {
	if value == nil {
		*a = UUIDArray{}
		return nil
	}
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("failed to scan uuid array: not a string")
	}
	
	str = strings.TrimPrefix(str, "{")
	str = strings.TrimSuffix(str, "}")
	
	if str == "" {
		*a = UUIDArray{}
		return nil
	}
	
	*a = UUIDArray(strings.Split(str, ","))
	return nil
}

func (a UUIDArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	for _, v := range a {
		if v == "" {
			return nil, fmt.Errorf("empty uuid in array")
		}
		if _, err := uuid.Parse(v); err != nil {
			return nil, fmt.Errorf("invalid uuid in array: %s", v)
		}
	}
	return "{" + strings.Join(a, ",") + "}", nil
}

type User struct {
	ID             string      `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	Username       string      `gorm:"uniqueIndex;not null"`
	Password       string      `gorm:"not null"`
	Firstname      string      `gorm:"not null"`
	Lastname       string      `gorm:"not null"`
	IsAdmin        bool        `gorm:"default:false"`
	Email          string      `gorm:"not null"`
	ProfilePicture string
	CoverPicture   string
	About          string
	LivesIn        string
	WorksAt        string
	Relationship   string
	Country        string
	Followers      StringArray `gorm:"type:text[]"`
	Following      StringArray `gorm:"type:text[]"`
	Friends        StringArray `gorm:"type:text[]"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Posts          []Post      `gorm:"foreignKey:UserID"`
	Chats          []Chat      `gorm:"many2many:user_chats"`
	Messages       []Message   `gorm:"foreignKey:SenderID"`
	Comments       []Comment   `gorm:"foreignKey:UserID"`
}

type Post struct {
	ID           string              `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	UserID       string              `gorm:"type:uuid;not null"`
	Desc         string
	Reactions    map[string][]string `gorm:"serializer:json"`
	CommentCount int
	Image        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Comments     []Comment           `gorm:"foreignKey:PostID"`
}

type Comment struct {
	ID         string              `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	PostID     string              `gorm:"type:uuid;not null"`
	UserID     string              `gorm:"type:uuid;not null"`
	Text       string              `gorm:"not null"`
	ParentID   *string             `gorm:"type:uuid"`
	Reactions  map[string][]string `gorm:"serializer:json"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Chat struct {
	ID        string      `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	Members   UUIDArray   `gorm:"type:uuid[]"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message   `gorm:"foreignKey:ChatID"`
}

type Message struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	ChatID    string    `gorm:"type:uuid"`
	SenderID  string    `gorm:"type:uuid"`
	Text      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type FriendRequest struct {
	ID          string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	SenderID    string    `gorm:"type:uuid;not null"`
	ReceiverID  string    `gorm:"type:uuid;not null"`
	Status      string    `gorm:"not null;default:pending"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Notification struct {
	ID          string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	UserID      string    `gorm:"type:uuid;not null"`
	Type        string    `gorm:"not null"`
	FromUserID  string    `gorm:"type:uuid;not null"`
	PostID      *string   `gorm:"type:uuid"`
	CommentID   *string   `gorm:"type:uuid"`
	Message     string    `gorm:"not null"`
	Read        bool      `gorm:"default:false"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Product struct {
	ID         string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	Title      string
	MobileIcon string
	DesktopIcon string
	BrandIcon  string
	CreatedAt  time.Time
	UpdatedAt time.Time
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == "" {
		u.ID = uuid.New().String()
	}
	return
}

func (p *Post) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return
}

func (c *Chat) BeforeCreate(tx *gorm.DB) (err error) {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return
}

func (m *Message) BeforeCreate(tx *gorm.DB) (err error) {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return
}

func (f *FriendRequest) BeforeCreate(tx *gorm.DB) (err error) {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return
}

func (n *Notification) BeforeCreate(tx *gorm.DB) (err error) {
	if n.ID == "" {
		n.ID = uuid.New().String()
	}
	return
}

func (p *Product) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return
}

func (c *Comment) BeforeCreate(tx *gorm.DB) (err error) {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return
}