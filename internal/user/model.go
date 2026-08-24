// Package user implements the user domain: registration, login, profile management.
package user

import "time"

// User is the database model for the users table.
type User struct {
	ID           int64     `db:"id"            json:"id"`
	Username     string    `db:"username"       json:"username"`
	Email        string    `db:"email"          json:"email"`
	PasswordHash string    `db:"password_hash"  json:"-"` // never serialise the hash
	AvatarURL    string    `db:"avatar_url"     json:"avatar_url"`
	CreatedAt    time.Time `db:"created_at"     json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"     json:"updated_at"`
}

// ── Request / response DTOs ───────────────────────────────────────────────────

// RegisterRequest is the body for POST /auth/register.
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// LoginRequest is the body for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// UpdateProfileRequest is the body for PATCH /users/me.
type UpdateProfileRequest struct {
	Username  string `json:"username"   binding:"omitempty,min=3,max=50"`
	AvatarURL string `json:"avatar_url" binding:"omitempty,url"`
}

// ProfileResponse is the public view of a user (no password hash).
type ProfileResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	AvatarURL string    `json:"avatar_url"`
	CreatedAt time.Time `json:"created_at"`
}

// ToProfile converts a User to its public ProfileResponse.
func (u *User) ToProfile() *ProfileResponse {
	return &ProfileResponse{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		AvatarURL: u.AvatarURL,
		CreatedAt: u.CreatedAt,
	}
}
