package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type fakeUserRepository struct {
	user             *User
	createErr        error
	getByIDErr       error
	getByEmailErr    error
	updateErr        error
	deleteAccountErr error
	created          bool
	updated          bool
	deletedAccountID int64
}

func (f *fakeUserRepository) Create(_ context.Context, u *User) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = true
	u.ID = 42
	u.CreatedAt = time.Now()
	u.UpdatedAt = u.CreatedAt
	f.user = u
	return nil
}

func (f *fakeUserRepository) GetByID(_ context.Context, _ int64) (*User, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	if f.user == nil {
		return nil, apperror.ErrNotFound
	}
	return f.user, nil
}

func (f *fakeUserRepository) GetByEmail(_ context.Context, _ string) (*User, error) {
	if f.getByEmailErr != nil {
		return nil, f.getByEmailErr
	}
	if f.user == nil {
		return nil, apperror.ErrNotFound
	}
	return f.user, nil
}

func (f *fakeUserRepository) GetByUsername(context.Context, string) (*User, error) {
	return f.user, nil
}

func (f *fakeUserRepository) Update(_ context.Context, u *User) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = true
	f.user = u
	return nil
}

func (f *fakeUserRepository) Delete(context.Context, int64) error {
	return nil
}

func (f *fakeUserRepository) DeleteAccount(_ context.Context, id int64) error {
	f.deletedAccountID = id
	return f.deleteAccountErr
}

func testConfig() *config.Config {
	return &config.Config{
		JWTAccessSecret:  "access-secret",
		JWTRefreshSecret: "refresh-secret",
		JWTAccessTTL:     15 * time.Minute,
		JWTRefreshTTL:    24 * time.Hour,
	}
}

func TestServiceRegisterHashesPassword(t *testing.T) {
	repo := &fakeUserRepository{}
	svc := NewService(repo, testConfig())

	pair, err := svc.Register(context.Background(), &RegisterRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("Register() returned an incomplete token pair")
	}
	if !repo.created || repo.user.PasswordHash == "correct horse battery staple" || repo.user.PasswordHash == "" {
		t.Fatal("Register() did not persist a bcrypt password hash")
	}
}

func TestServiceLoginReturnsUnauthorizedForInvalidCredentials(t *testing.T) {
	repo := &fakeUserRepository{user: &User{PasswordHash: "$2a$12$invalid"}}
	svc := NewService(repo, testConfig())

	_, err := svc.Login(context.Background(), &LoginRequest{Email: "alice@example.com", Password: "wrong"})
	if !errors.Is(err, apperror.ErrUnauthorized) {
		t.Fatalf("Login() error = %v, want unauthorized", err)
	}
}

func TestServiceDeleteAccountEnforcesOwnership(t *testing.T) {
	repo := &fakeUserRepository{deleteAccountErr: apperror.ErrAccountOwnsRooms}
	svc := NewService(repo, testConfig())

	err := svc.DeleteAccount(context.Background(), 7, 7)
	if !errors.Is(err, apperror.ErrAccountOwnsRooms) {
		t.Fatalf("DeleteAccount() error = %v, want ownership conflict", err)
	}
	if repo.deletedAccountID != 7 {
		t.Fatalf("DeleteAccount() passed id %d, want 7", repo.deletedAccountID)
	}
}

func TestServiceDeleteAccountRejectsDifferentRequester(t *testing.T) {
	repo := &fakeUserRepository{}
	svc := NewService(repo, testConfig())

	if err := svc.DeleteAccount(context.Background(), 1, 2); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("DeleteAccount() error = %v, want forbidden", err)
	}
	if repo.deletedAccountID != 0 {
		t.Fatal("DeleteAccount() called repository for a different requester")
	}
}
