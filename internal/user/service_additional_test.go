package user

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"golang.org/x/crypto/bcrypt"
)

func TestServiceRegisterPropagatesRepositoryConflict(t *testing.T) {
	repo := &fakeUserRepository{createErr: apperror.ErrConflict}
	svc := NewService(repo, testConfig())

	_, err := svc.Register(context.Background(), &RegisterRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "correct horse battery staple",
	})
	if !errors.Is(err, apperror.ErrConflict) {
		t.Fatalf("Register() error = %v, want conflict", err)
	}
}

func TestServiceLoginReturnsTokenPairForValidCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	repo := &fakeUserRepository{user: &User{ID: 42, PasswordHash: string(hash)}}
	svc := NewService(repo, testConfig())

	pair, err := svc.Login(context.Background(), &LoginRequest{
		Email:    "alice@example.com",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("Login() returned an incomplete token pair")
	}
}

func TestServiceRefreshTokensRejectsDeletedUser(t *testing.T) {
	pair, err := middleware.GenerateTokenPair(testConfig(), 42)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error = %v", err)
	}
	repo := &fakeUserRepository{getByIDErr: apperror.ErrNotFound}
	svc := NewService(repo, testConfig())

	_, err = svc.RefreshTokens(context.Background(), pair.RefreshToken)
	if !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("RefreshTokens() error = %v, want unauthorized", err)
	}
}

func TestServiceRefreshTokensReturnsNewPair(t *testing.T) {
	pair, err := middleware.GenerateTokenPair(testConfig(), 42)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error = %v", err)
	}
	repo := &fakeUserRepository{user: &User{ID: 42}}
	svc := NewService(repo, testConfig())

	refreshed, err := svc.RefreshTokens(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshTokens() error = %v", err)
	}
	if refreshed.AccessToken == "" || refreshed.RefreshToken == "" {
		t.Fatal("RefreshTokens() returned an incomplete token pair")
	}
}

func TestServiceGetAndUpdateProfile(t *testing.T) {
	repo := &fakeUserRepository{user: &User{ID: 42, Username: "alice", Email: "alice@example.com"}}
	svc := NewService(repo, testConfig())

	profile, err := svc.GetProfile(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if profile.Username != "alice" || profile.Email != "alice@example.com" {
		t.Fatalf("GetProfile() = %+v", profile)
	}

	updated, err := svc.UpdateProfile(context.Background(), 42, &UpdateProfileRequest{
		Username:  "alice-updated",
		AvatarURL: "https://example.com/avatar.png",
	})
	if err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	if updated.Username != "alice-updated" || updated.AvatarURL != "https://example.com/avatar.png" || !repo.updated {
		t.Fatalf("UpdateProfile() = %+v; repository updated = %t", updated, repo.updated)
	}
}

func TestServiceUpdateProfilePropagatesRepositoryFailure(t *testing.T) {
	repo := &fakeUserRepository{
		user:      &User{ID: 42, Username: "alice"},
		updateErr: errors.New("database unavailable"),
	}
	svc := NewService(repo, testConfig())

	_, err := svc.UpdateProfile(context.Background(), 42, &UpdateProfileRequest{Username: "alice-new"})
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("UpdateProfile() error = %v, want repository failure", err)
	}
}
