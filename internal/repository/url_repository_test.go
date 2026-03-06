package repository_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emsbt/url-shortener/internal/model"
	"github.com/emsbt/url-shortener/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dbCounter atomic.Int64

func newTestRepo(t *testing.T) repository.URLRepository {
	t.Helper()
	// Use a unique in-memory database per test to prevent cross-test state sharing.
	n := dbCounter.Add(1)
	dsn := fmt.Sprintf("file:testdb%d?mode=memory&cache=shared", n)
	repo, err := repository.NewSQLiteRepository(dsn)
	require.NoError(t, err)
	return repo
}

func makeURL(id string) *model.URL {
	return &model.URL{
		ID:          id,
		ShortURL:    "http://localhost:8080/" + id,
		OriginalURL: "https://example.com/path",
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
		ClickCount:  0,
	}
}

func TestCreate_and_GetByID(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	u := makeURL("abc123")
	require.NoError(t, repo.Create(ctx, u))

	got, err := repo.GetByID(ctx, "abc123")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, u.OriginalURL, got.OriginalURL)
	assert.Equal(t, u.ShortURL, got.ShortURL)
	assert.Equal(t, int64(0), got.ClickCount)
}

func TestCreate_DuplicateID(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	u := makeURL("dup001")
	require.NoError(t, repo.Create(ctx, u))

	err := repo.Create(ctx, u)
	assert.ErrorIs(t, err, repository.ErrDuplicateID)
}

func TestGetByID_NotFound(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "nonexistent")
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestIncrementClickCount(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	u := makeURL("clk001")
	require.NoError(t, repo.Create(ctx, u))

	require.NoError(t, repo.IncrementClickCount(ctx, "clk001"))
	require.NoError(t, repo.IncrementClickCount(ctx, "clk001"))

	got, err := repo.GetByID(ctx, "clk001")
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.ClickCount)
}

func TestIncrementClickCount_NotFound(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	err := repo.IncrementClickCount(ctx, "missing")
	assert.ErrorIs(t, err, repository.ErrNotFound)
}

func TestList_Pagination(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := range 5 {
		id := "pg" + string(rune('a'+i))
		require.NoError(t, repo.Create(ctx, makeURL(id)))
	}

	urls, total, err := repo.List(ctx, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, urls, 3)

	urls2, total2, err := repo.List(ctx, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total2)
	assert.Len(t, urls2, 2)
}

func TestExistsID(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	ok, err := repo.ExistsID(ctx, "xyz")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, repo.Create(ctx, makeURL("xyz")))

	ok, err = repo.ExistsID(ctx, "xyz")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCreate_WithExpirationDate(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	u := makeURL("exp001")
	u.ExpirationDate = &exp

	require.NoError(t, repo.Create(ctx, u))

	got, err := repo.GetByID(ctx, "exp001")
	require.NoError(t, err)
	require.NotNil(t, got.ExpirationDate)
	assert.Equal(t, exp, *got.ExpirationDate)
}
