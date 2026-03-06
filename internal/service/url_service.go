package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"github.com/emsbt/url-shortener/internal/model"
	"github.com/emsbt/url-shortener/internal/repository"
)

const (
	base62Chars    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	defaultIDLen   = 7
	maxRetries     = 10
)

// ErrInvalidURL is returned when the original URL fails validation.
var ErrInvalidURL = errors.New("invalid url")

// ErrURLNotFound is returned when the short ID does not exist.
var ErrURLNotFound = errors.New("url not found")

// ErrURLExpired is returned when the short URL has passed its expiration date.
var ErrURLExpired = errors.New("url expired")

// ErrAliasConflict is returned when a requested custom alias is already taken.
var ErrAliasConflict = errors.New("alias already in use")

// URLService defines the business-logic contract for URL shortening.
type URLService interface {
	Create(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error)
	GetByID(ctx context.Context, id string) (*model.URLDetailsResponse, error)
	Redirect(ctx context.Context, id string) (string, error)
	List(ctx context.Context, page, size int) (*model.ListURLsResponse, error)
}

type urlService struct {
	repo    repository.URLRepository
	baseURL string
	logger  *slog.Logger
}

// NewURLService constructs a URLService backed by the given repository.
func NewURLService(repo repository.URLRepository, baseURL string, logger *slog.Logger) URLService {
	return &urlService{
		repo:    repo,
		baseURL: strings.TrimRight(baseURL, "/"),
		logger:  logger,
	}
}

// Create validates the request, generates (or uses) a short ID, and persists
// the new URL record.
func (s *urlService) Create(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error) {
	if err := validateURL(req.OriginalURL); err != nil {
		return nil, err
	}

	var id string
	if req.CustomAlias != "" {
		exists, err := s.repo.ExistsID(ctx, req.CustomAlias)
		if err != nil {
			return nil, fmt.Errorf("check alias: %w", err)
		}
		if exists {
			return nil, ErrAliasConflict
		}
		id = req.CustomAlias
	} else {
		var err error
		id, err = s.generateUniqueID(ctx)
		if err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	u := &model.URL{
		ID:             id,
		ShortURL:       s.baseURL + "/" + id,
		OriginalURL:    req.OriginalURL,
		CreatedAt:      now,
		ExpirationDate: req.ExpirationDate,
		ClickCount:     0,
	}

	if err := s.repo.Create(ctx, u); err != nil {
		if errors.Is(err, repository.ErrDuplicateID) {
			return nil, ErrAliasConflict
		}
		return nil, fmt.Errorf("create url: %w", err)
	}

	s.logger.InfoContext(ctx, "url created",
		slog.String("id", id),
		slog.String("original_url", req.OriginalURL),
	)

	return &model.CreateURLResponse{
		ID:             u.ID,
		ShortURL:       u.ShortURL,
		OriginalURL:    u.OriginalURL,
		CreatedAt:      u.CreatedAt,
		ExpirationDate: u.ExpirationDate,
	}, nil
}

// GetByID fetches URL details by short ID.
func (s *urlService) GetByID(ctx context.Context, id string) (*model.URLDetailsResponse, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrURLNotFound
		}
		return nil, fmt.Errorf("get url: %w", err)
	}

	return &model.URLDetailsResponse{
		ID:             u.ID,
		ShortURL:       u.ShortURL,
		OriginalURL:    u.OriginalURL,
		CreatedAt:      u.CreatedAt,
		ExpirationDate: u.ExpirationDate,
		ClickCount:     u.ClickCount,
	}, nil
}

// Redirect resolves the original URL for a redirect and increments the click
// counter. Returns ErrURLNotFound or ErrURLExpired as appropriate.
func (s *urlService) Redirect(ctx context.Context, id string) (string, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.logger.WarnContext(ctx, "redirect: url not found", slog.String("id", id))
			return "", ErrURLNotFound
		}
		return "", fmt.Errorf("get url for redirect: %w", err)
	}

	if u.ExpirationDate != nil && time.Now().UTC().After(*u.ExpirationDate) {
		s.logger.WarnContext(ctx, "redirect: url expired",
			slog.String("id", id),
			slog.Time("expired_at", *u.ExpirationDate),
		)
		return "", ErrURLExpired
	}

	if err := s.repo.IncrementClickCount(ctx, id); err != nil {
		// Non-fatal: log and continue
		s.logger.ErrorContext(ctx, "increment click count failed",
			slog.String("id", id),
			slog.String("error", err.Error()),
		)
	}

	s.logger.InfoContext(ctx, "redirect",
		slog.String("id", id),
		slog.String("original_url", u.OriginalURL),
	)

	return u.OriginalURL, nil
}

// List returns a paginated list of all URLs.
func (s *urlService) List(ctx context.Context, page, size int) (*model.ListURLsResponse, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	urls, total, err := s.repo.List(ctx, page, size)
	if err != nil {
		return nil, fmt.Errorf("list urls: %w", err)
	}

	details := make([]model.URLDetailsResponse, len(urls))
	for i, u := range urls {
		details[i] = model.URLDetailsResponse{
			ID:             u.ID,
			ShortURL:       u.ShortURL,
			OriginalURL:    u.OriginalURL,
			CreatedAt:      u.CreatedAt,
			ExpirationDate: u.ExpirationDate,
			ClickCount:     u.ClickCount,
		}
	}

	return &model.ListURLsResponse{
		Data:  details,
		Page:  page,
		Size:  size,
		Total: total,
	}, nil
}

// ---- helpers ----

// validateURL checks that originalURL is non-empty, well-formed, and uses
// http or https scheme.
func validateURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidURL)
	}

	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidURL, err.Error())
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: scheme must be http or https", ErrInvalidURL)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%w: host is required", ErrInvalidURL)
	}

	return nil
}

// generateUniqueID generates a random Base62 ID that does not already exist in
// the repository. It retries up to maxRetries times before returning an error.
func (s *urlService) generateUniqueID(ctx context.Context) (string, error) {
	for range maxRetries {
		id := randomBase62(defaultIDLen)
		exists, err := s.repo.ExistsID(ctx, id)
		if err != nil {
			return "", fmt.Errorf("check id existence: %w", err)
		}
		if !exists {
			return id, nil
		}
	}
	return "", errors.New("could not generate unique id after max retries")
}

// randomBase62 returns a random alphanumeric string of the given length.
func randomBase62(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = base62Chars[rand.IntN(len(base62Chars))]
	}
	return string(b)
}
