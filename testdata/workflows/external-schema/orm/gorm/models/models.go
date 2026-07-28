package models

import "gorm.io/gorm"

// User owns pets and has a provider-generated unique email index.
type User struct {
	gorm.Model
	Email string `gorm:"type:text;not null;uniqueIndex:idx_users_email"`
	Pets  []Pet  `gorm:"constraint:OnDelete:CASCADE;"`
}

// Pet belongs to one user through the generated user_id foreign key.
type Pet struct {
	gorm.Model
	UserID uint `gorm:"not null"`
	User   User `gorm:"constraint:OnDelete:CASCADE;"`
}
