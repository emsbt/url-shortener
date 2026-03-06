package service_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emsbt/url-shortener/internal/model"
	"github.com/emsbt/url-shortener/internal/repository"
	"github.com/emsbt/url-shortener/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dbCounter atomic.Int64

func newTestService(t *testing.T) service.URLService {
	t.Helper()
	n := dbCounter.Add(1)
	dsn := fmt.Sprintf("file:svcdb%d?mode=memory&cache=shared", n)
	repo, err := repository.NewSQLiteRepository(dsn)
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	return service.NewURLService(repo, "http://localhost:8080", logger)
}

func TestCreate_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &model.CreateURLRequest{
		OriginalURL: "https://example.com/path",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, "http://localhost:8080/"+resp.ID, resp.ShortURL)
	assert.Equal(t, "https://example.com/path", resp.OriginalURL)
}

func TestCreate_CustomAlias(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &model.CreateURLRequest{
		OriginalURL: "https://example.com",
		CustomAlias: "my-alias",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-alias", resp.ID)
}

func TestCreate_CustomAlias_Conflict(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	req := &model.CreateURLRequest{
		OriginalURL: "https://example.com",
		CustomAlias: "taken",
	}
	_, err := svc.Create(ctx, req)
	require.NoError(t, err)

	_, err = svc.Create(ctx, req)
	assert.ErrorIs(t, err, service.ErrAliasConflict)
}

func TestCreate_InvalidURL_Empty(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Create(context.Background(), &model.CreateURLRequest{OriginalURL: ""})
	assert.ErrorIs(t, err, service.ErrInvalidURL)
}

func TestCreate_InvalidURL_NoScheme(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Create(context.Background(), &model.CreateURLRequest{OriginalURL: "example.com"})
	assert.ErrorIs(t, err, service.ErrInvalidURL)
}

func TestCreate_InvalidURL_FTPScheme(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Create(context.Background(), &model.CreateURLRequest{OriginalURL: "ftp://example.com"})
	assert.ErrorIs(t, err, service.ErrInvalidURL)
}

func TestGetByID_NotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetByID(context.Background(), "nope")
	assert.ErrorIs(t, err, service.ErrURLNotFound)
}

func TestRedirect_Success(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &model.CreateURLRequest{OriginalURL: "https://example.com"})
	require.NoError(t, err)

	target, err := svc.Redirect(ctx, resp.ID)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", target)
}

func TestRedirect_NotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Redirect(context.Background(), "missing")
	assert.ErrorIs(t, err, service.ErrURLNotFound)
}

func TestRedirect_Expired(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Hour)
	resp, err := svc.Create(ctx, &model.CreateURLRequest{
		OriginalURL:    "https://example.com",
		ExpirationDate: &past,
	})
	require.NoError(t, err)

	_, err = svc.Redirect(ctx, resp.ID)
	assert.ErrorIs(t, err, service.ErrURLExpired)
}

func TestRedirect_ClickCountIncremented(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &model.CreateURLRequest{OriginalURL: "https://example.com"})
	require.NoError(t, err)

	for range 3 {
		_, err = svc.Redirect(ctx, resp.ID)
		require.NoError(t, err)
	}

	details, err := svc.GetByID(ctx, resp.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), details.ClickCount)
}

func TestList_Pagination(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	for i := range 7 {
		_, err := svc.Create(ctx, &model.CreateURLRequest{
			OriginalURL: "https://example.com/" + string(rune('a'+i)),
		})
		require.NoError(t, err)
	}

	result, err := svc.List(ctx, 1, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(7), result.Total)
	assert.Len(t, result.Data, 5)
	assert.Equal(t, 1, result.Page)
	assert.Equal(t, 5, result.Size)

	result2, err := svc.List(ctx, 2, 5)
	require.NoError(t, err)
	assert.Len(t, result2.Data, 2)
}

func TestCreate_WithExpiration(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	future := time.Now().Add(24 * time.Hour)
	resp, err := svc.Create(ctx, &model.CreateURLRequest{
		OriginalURL:    "https://example.com",
		ExpirationDate: &future,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.ExpirationDate)

	// Should redirect fine (not expired yet)
	_, err = svc.Redirect(ctx, resp.ID)
	require.NoError(t, err)
}

func TestCreate_IDNotEmpty(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	for range 20 {
		resp, err := svc.Create(ctx, &model.CreateURLRequest{
			OriginalURL: "https://example.com",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.ID)
		assert.GreaterOrEqual(t, len(resp.ID), 6)
		assert.LessOrEqual(t, len(resp.ID), 8)
	}
}

func TestCreate_InvalidURL_MalformedURL(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Create(context.Background(), &model.CreateURLRequest{OriginalURL: "://bad-url"})
	assert.True(t, errors.Is(err, service.ErrInvalidURL))
}
