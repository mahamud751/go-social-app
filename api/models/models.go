package models

import (
	"database/sql/driver"
	"time"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"fmt"
	"strings"

)

// StringArray is a custom type to handle PostgreSQL text arrays
type StringArray []string

// Scan implements the sql.Scanner interface
func (a *StringArray) Scan(value interface{}) error {
	if value == nil {
		*a = StringArray{}
		return nil
	}
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("failed to scan array: not a string")
	}
	
	// Remove curly braces
	str = strings.TrimPrefix(str, "{")
	str = strings.TrimSuffix(str, "}")
	
	if str == "" {
		*a = StringArray{}
		return nil
	}
	
	*a = StringArray(strings.Split(str, ","))
	return nil
}

// Value implements the driver.Valuer interface
func (a StringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	return "{" + strings.Join(a, ",") + "}", nil
}

type User struct {
	ID             string     `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	Username       string     `gorm:"uniqueIndex;not null"`
	Password       string     `gorm:"not null"`
	Firstname      string     `gorm:"not null"`
	Lastname       string     `gorm:"not null"`
	IsAdmin        bool       `gorm:"default:false"`
	Email          string     `gorm:"not null"`
	ProfilePicture string
	CoverPicture   string
	About          string
	LivesIn        string
	WorksAt        string
	Relationship   string
	Country        string
	Followers      StringArray `gorm:"type:text[]"`
	Following      StringArray `gorm:"type:text[]"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Posts          []Post    `gorm:"foreignKey:UserID"`
	Chats          []Chat    `gorm:"many2many:user_chats"`
	Messages       []Message `gorm:"foreignKey:SenderID"`
}

type Post struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	UserID    string    `gorm:"type:uuid;not null"`
	Desc      string
	Likes     []string  `gorm:"type:uuid[]"`
	Image     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Chat struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	Members   []string  `gorm:"type:uuid[]"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message `gorm:"foreignKey:ChatID"`
}

type Message struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	ChatID    string    `gorm:"type:uuid"`
	SenderID  string    `gorm:"type:uuid"`
	Text      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Product struct {
	ID         string    `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	Title      string
	MobileIcon string
	DesktopIcon string
	BrandIcon  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
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

func (p *Product) BeforeCreate(tx *gorm.DB) (err error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return
}