package user

import (
	"context"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
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
	// Hash the password with bcrypt (cost 12 is a good balance of security/speed).
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		return nil, fmt.Errorf("user service Register: bcrypt: %w", err)
	}

	u := &User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: string(hash),
	}

	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err // already wrapped (ErrConflict or internal)
	}

	return middleware.GenerateTokenPair(s.cfg, u.ID)
}

// Login verifies credentials and returns a token pair on success.
func (s *Service) Login(ctx context.Context, req *LoginRequest) (*middleware.TokenPair, error) {
	u, err := s.repo.GetByEmail(ctx, req.Email)
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
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if req.Username != "" {
		u.Username = req.Username
	}
	if req.AvatarURL != "" {
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
	return s.repo.Delete(ctx, targetID)
}
