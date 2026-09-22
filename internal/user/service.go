package user

import (
	"context"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"golang.org/x/crypto/bcrypt"
)

// Service encapsulates business logic for the user domain.
type Service struct {
	repo Repository
	cfg  *config.Config
}

// NewService creates a new user Service.
func NewService(repo Repository, cfg *config.Config) *Service {
	return &Service{repo: repo, cfg: cfg}
}

// Register creates a new user account and returns a token pair.
func (s *Service) Register(ctx context.Context, req *RegisterRequest) (*middleware.TokenPair, error) {
	if req == nil {
		return nil, apperror.ErrInvalidRequest
	}
	username, validUsername := normalizeUsername(req.Username)
	email, validEmail := normalizeEmail(req.Email)
	if !validUsername || !validEmail || !validPassword(req.Password) {
		return nil, apperror.ErrInvalidRequest
	}

	// Hash the password with bcrypt (cost 12 is a good balance of security/speed).
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		return nil, fmt.Errorf("user service Register: bcrypt: %w", err)
	}

	u := &User{
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
	}

	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err // already wrapped (ErrConflict or internal)
	}

	return middleware.GenerateTokenPair(s.cfg, u.ID)
}

// Login verifies credentials and returns a token pair on success.
func (s *Service) Login(ctx context.Context, req *LoginRequest) (*middleware.TokenPair, error) {
	if req == nil || req.Password == "" {
		return nil, apperror.ErrInvalidRequest
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		return nil, apperror.ErrInvalidRequest
	}
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		// Return generic unauthorized — don't leak whether the email exists.
		return nil, apperror.ErrUnauthorized
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		return nil, apperror.ErrUnauthorized
	}

	return middleware.GenerateTokenPair(s.cfg, u.ID)
}

// RefreshTokens validates a refresh token and issues a new pair.
func (s *Service) RefreshTokens(ctx context.Context, refreshToken string) (*middleware.TokenPair, error) {
	claims, err := middleware.ParseRefreshToken(s.cfg, refreshToken)
	if err != nil {
		return nil, apperror.ErrUnauthorized
	}

	// Ensure the user still exists (could have been deleted).
	if _, err := s.repo.GetByID(ctx, claims.UserID); err != nil {
		return nil, apperror.ErrUnauthorized
	}

	return middleware.GenerateTokenPair(s.cfg, claims.UserID)
}

// GetProfile returns the public profile of a user.
func (s *Service) GetProfile(ctx context.Context, userID int64) (*ProfileResponse, error) {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return u.ToProfile(), nil
}

// UpdateProfile applies profile changes and returns the updated profile.
func (s *Service) UpdateProfile(ctx context.Context, userID int64, req *UpdateProfileRequest) (*ProfileResponse, error) {
	if req == nil {
		return nil, apperror.ErrInvalidRequest
	}
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if req.Username != "" {
		username, ok := normalizeUsername(req.Username)
		if !ok {
			return nil, apperror.ErrInvalidRequest
		}
		u.Username = username
	}
	if req.AvatarURL != "" {
		if !validAvatarURL(req.AvatarURL) {
			return nil, apperror.ErrInvalidRequest
		}
		u.AvatarURL = req.AvatarURL
	}

	if err := s.repo.Update(ctx, u); err != nil {
		return nil, err
	}
	return u.ToProfile(), nil
}

// DeleteAccount removes a user account. Only the account owner may do this.
func (s *Service) DeleteAccount(ctx context.Context, requesterID, targetID int64) error {
	if requesterID != targetID {
		return apperror.ErrForbidden
	}
	return s.repo.DeleteAccount(ctx, targetID)
}
