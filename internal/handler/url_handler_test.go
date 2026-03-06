package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emsbt/url-shortener/internal/handler"
	"github.com/emsbt/url-shortener/internal/model"
	"github.com/emsbt/url-shortener/internal/repository"
	"github.com/emsbt/url-shortener/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dbCounter atomic.Int64

func setupHandler(t *testing.T) (*handler.URLHandler, *chi.Mux) {
	t.Helper()
	n := dbCounter.Add(1)
	dsn := fmt.Sprintf("file:handlerdb%d?mode=memory&cache=shared", n)
	repo, err := repository.NewSQLiteRepository(dsn)
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := service.NewURLService(repo, "http://localhost:8080", logger)
	h := handler.NewURLHandler(svc, logger)

	r := chi.NewRouter()
	r.Post("/v1/urls", h.CreateURL)
	r.Get("/v1/urls", h.ListURLs)
	r.Get("/v1/urls/{id}", h.GetURL)
	r.Get("/{id}", h.RedirectURL)

	return h, r
}

func TestCreateURL_Success(t *testing.T) {
	_, router := setupHandler(t)

	body := `{"originalUrl":"https://example.com/some/path"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp model.CreateURLResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, "https://example.com/some/path", resp.OriginalURL)
	assert.Contains(t, resp.ShortURL, resp.ID)
}

func TestCreateURL_InvalidURL(t *testing.T) {
	_, router := setupHandler(t)

	body := `{"originalUrl":"not-a-url"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp model.ErrorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Equal(t, "INVALID_URL", errResp.Error.Code)
}

func TestCreateURL_EmptyBody(t *testing.T) {
	_, router := setupHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateURL_CustomAlias(t *testing.T) {
	_, router := setupHandler(t)

	body := `{"originalUrl":"https://example.com","customAlias":"my-link"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp model.CreateURLResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "my-link", resp.ID)
}

func TestCreateURL_AliasConflict(t *testing.T) {
	_, router := setupHandler(t)

	body := `{"originalUrl":"https://example.com","customAlias":"conflict"}`
	req1 := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req1)

	req2 := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusConflict, rec2.Code)
}

func TestGetURL_Success(t *testing.T) {
	_, router := setupHandler(t)

	// Create first
	body := `{"originalUrl":"https://example.com"}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var createResp model.CreateURLResponse
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&createResp))

	// Get
	getReq := httptest.NewRequest(http.MethodGet, "/v1/urls/"+createResp.ID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	assert.Equal(t, http.StatusOK, getRec.Code)

	var detailResp model.URLDetailsResponse
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detailResp))
	assert.Equal(t, createResp.ID, detailResp.ID)
}

func TestGetURL_NotFound(t *testing.T) {
	_, router := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/urls/doesnotexist", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var errResp model.ErrorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Equal(t, "URL_NOT_FOUND", errResp.Error.Code)
}

func TestRedirectURL_Success(t *testing.T) {
	_, router := setupHandler(t)

	body := `{"originalUrl":"https://example.com","customAlias":"redir1"}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), createReq)

	req := httptest.NewRequest(http.MethodGet, "/redir1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "https://example.com", rec.Header().Get("Location"))
}

func TestRedirectURL_NotFound(t *testing.T) {
	_, router := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/nothere", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRedirectURL_Expired(t *testing.T) {
	_, router := setupHandler(t)

	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"originalUrl":"https://example.com","customAlias":"exprd","expirationDate":"` + past + `"}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), createReq)

	req := httptest.NewRequest(http.MethodGet, "/exprd", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusGone, rec.Code)
}

func TestListURLs(t *testing.T) {
	_, router := setupHandler(t)

	for i := range 3 {
		body := `{"originalUrl":"https://example.com/` + string(rune('a'+i)) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/urls?page=1&size=10", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var listResp model.ListURLsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&listResp))
	assert.Equal(t, int64(3), listResp.Total)
	assert.Len(t, listResp.Data, 3)
}

func TestRedirectURL_ClickCount(t *testing.T) {
	_, router := setupHandler(t)

	body := `{"originalUrl":"https://example.com","customAlias":"clktest"}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/urls", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), createReq)

	for range 5 {
		req := httptest.NewRequest(http.MethodGet, "/clktest", nil)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/urls/clktest", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	var detail model.URLDetailsResponse
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	assert.Equal(t, int64(5), detail.ClickCount)
}

// withChiParam injects a chi URL parameter into the request context.
func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
