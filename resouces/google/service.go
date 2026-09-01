package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vertrai/hub/resouces/schema"
	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service owns Google account creation, allocation, authorization and
// Workspace operations. The resouces root package only exposes HTTP routes.
type Service struct {
	db       *gorm.DB
	creator  GoogleUserCreator
	issuer   GoogleTokenIssuer
	domain   string
	createMu sync.Mutex
}

func NewService(db *gorm.DB, creator GoogleUserCreator, issuer GoogleTokenIssuer, domain string) *Service {
	return &Service{db: db, creator: creator, issuer: issuer, domain: strings.ToLower(strings.TrimSpace(domain))}
}

func (s *Service) Domain() string { return s.domain }

func (s *Service) CreateAccount(ctx context.Context, email, password, givenName, familyName string) (schema.GoogleAccount, error) {
	googleID, err := s.creator.Create(ctx, NewGoogleUser{Email: email, Password: password, GivenName: givenName, FamilyName: familyName})
	if err != nil {
		return schema.GoogleAccount{}, err
	}
	row := schema.GoogleAccount{ID: "gusr_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], Email: email, Password: password, GoogleUserID: googleID, Status: schema.StatusAvailable}
	if err := s.db.Create(&row).Error; err != nil {
		return schema.GoogleAccount{}, fmt.Errorf("save google account: %w", err)
	}
	return row, nil
}

func (s *Service) NextAccountEmail(domain string) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		number, err := rand.Int(rand.Reader, big.NewInt(900000))
		if err != nil {
			return "", err
		}
		email := fmt.Sprintf("user%06d@%s", number.Int64()+100000, strings.ToLower(strings.TrimSpace(domain)))
		var count int64
		if err := s.db.Model(&schema.GoogleAccount{}).Where("email = ?", email).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return email, nil
		}
	}
	return "", fmt.Errorf("failed to generate an unused google account email")
}

func (s *Service) ListAccounts() ([]schema.GoogleAccount, error) {
	var rows []schema.GoogleAccount
	err := s.db.Order("created_at desc").Find(&rows).Error
	return rows, err
}

// CreateAccounts uses the same production path for both administrator batch
// creation and automatic pool replenishment.
func (s *Service) CreateAccounts(ctx context.Context, count int, domain string) ([]schema.GoogleAccount, error) {
	if count <= 0 {
		return []schema.GoogleAccount{}, nil
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	created := make([]schema.GoogleAccount, 0, count)
	for len(created) < count {
		email, err := s.NextAccountEmail(domain)
		if err != nil {
			return created, err
		}
		password, err := RandomPassword()
		if err != nil {
			return created, err
		}
		account, err := s.CreateAccount(ctx, email, password, "Agent", strings.TrimSuffix(email, "@"+domain))
		if err != nil {
			return created, err
		}
		created = append(created, account)
	}
	return created, nil
}

func (s *Service) AssignAccount(accessKeyID string) (schema.GoogleAccount, error) {
	var account schema.GoogleAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var accessKey schema.AccessKey
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&accessKey, "id = ?", accessKeyID).Error; err != nil {
			return err
		}
		err := tx.Where("assigned_access_key_id = ?", accessKeyID).First(&account).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status = ? AND purpose = ?", schema.StatusAvailable, schema.GooglePurposeGeneral).Order("created_at").First(&account).Error; err != nil {
			return err
		}
		now := time.Now()
		account.Status = schema.StatusAssigned
		account.AssignedAccessKeyID = &accessKeyID
		account.AssignedAt = &now
		return tx.Save(&account).Error
	})
	return account, err
}

// AcquireAccount returns the account already owned by an API key, assigns one
// from the pool, or produces exactly one account when the pool is empty.
func (s *Service) AcquireAccount(ctx context.Context, accessKeyID string) (schema.GoogleAccount, error) {
	account, err := s.AssignAccount(accessKeyID)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return account, err
	}

	// Only one process-local request replenishes an empty pool at a time. After
	// entering the lock, retry allocation because another request may have
	// produced an account while this request was waiting.
	s.createMu.Lock()
	defer s.createMu.Unlock()
	account, err = s.AssignAccount(accessKeyID)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return account, err
	}
	if s.domain == "" {
		return schema.GoogleAccount{}, fmt.Errorf("automatically create google account: workspace domain is required")
	}
	if _, err := s.CreateAccounts(ctx, 1, s.domain); err != nil {
		return schema.GoogleAccount{}, fmt.Errorf("automatically create google account: %w", err)
	}
	return s.AssignAccount(accessKeyID)
}

func (s *Service) IssueToken(ctx context.Context, accessKeyID string) (*oauth2.Token, schema.GoogleAccount, error) {
	account, err := s.AcquireAccount(ctx, accessKeyID)
	if err != nil {
		return nil, schema.GoogleAccount{}, err
	}
	token, err := s.issuer.Issue(ctx, account.Email)
	return token, account, err
}

func (s *Service) httpClient(ctx context.Context, accessKeyID string) (*http.Client, schema.GoogleAccount, error) {
	token, account, err := s.IssueToken(ctx, accessKeyID)
	if err != nil {
		return nil, schema.GoogleAccount{}, err
	}
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(token)), account, nil
}

func (s *Service) SendGmail(ctx context.Context, accessKeyID, to, subject, body string) (string, string, schema.GoogleAccount, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil || address.Address != strings.TrimSpace(to) {
		return "", "", schema.GoogleAccount{}, fmt.Errorf("valid recipient email is required")
	}
	if strings.TrimSpace(subject) == "" {
		return "", "", schema.GoogleAccount{}, fmt.Errorf("subject is required")
	}
	client, account, err := s.httpClient(ctx, accessKeyID)
	if err != nil {
		return "", "", schema.GoogleAccount{}, err
	}
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", "", schema.GoogleAccount{}, err
	}
	raw := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s", account.Email, address.Address, mime.QEncoding.Encode("UTF-8", subject), body)
	sent, err := srv.Users.Messages.Send("me", &gmail.Message{Raw: base64.RawURLEncoding.EncodeToString([]byte(raw))}).Do()
	if err != nil {
		return "", "", schema.GoogleAccount{}, fmt.Errorf("send gmail: %w", err)
	}
	return sent.Id, sent.ThreadId, account, nil
}

func (s *Service) CreateDriveFolder(ctx context.Context, accessKeyID, name string) (*drive.File, schema.GoogleAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, schema.GoogleAccount{}, fmt.Errorf("folder name is required")
	}
	client, account, err := s.httpClient(ctx, accessKeyID)
	if err != nil {
		return nil, schema.GoogleAccount{}, err
	}
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, schema.GoogleAccount{}, err
	}
	created, err := srv.Files.Create(&drive.File{Name: name, MimeType: "application/vnd.google-apps.folder"}).Fields("id,name,mimeType,webViewLink").Do()
	if err != nil {
		return nil, schema.GoogleAccount{}, fmt.Errorf("create drive folder: %w", err)
	}
	return created, account, nil
}
