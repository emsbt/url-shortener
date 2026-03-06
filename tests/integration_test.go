package integration_test

import (
	"bytes"
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
	"github.com/emsbt/url-shortener/internal/middleware"
	"github.com/emsbt/url-shortener/internal/model"
	"github.com/emsbt/url-shortener/internal/repository"
	"github.com/emsbt/url-shortener/internal/service"
	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAPIKey = "test-api-key"

var dbCounter atomic.Int64

func buildTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	n := dbCounter.Add(1)
	dsn := fmt.Sprintf("file:integdb%d?mode=memory&cache=shared", n)
	repo, err := repository.NewSQLiteRepository(dsn)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := service.NewURLService(repo, "http://localhost:8080", logger)
	h := handler.NewURLHandler(svc, logger)

	r := chi.NewRouter()
	r.Use(chiMiddleware.RealIP)
	r.Use(middleware.Recoverer(logger))

	r.Get("/{id}", h.RedirectURL)

	r.Group(func(r chi.Router) {
		r.Use(middleware.APIKeyAuth(testAPIKey))
		r.Post("/v1/urls", h.CreateURL)
		r.Get("/v1/urls", h.ListURLs)
		r.Get("/v1/urls/{id}", h.GetURL)
	})

	return httptest.NewServer(r)
}

func apiPost(t *testing.T, server *httptest.Server, path, body, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func apiGet(t *testing.T, server *httptest.Server, path, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	require.NoError(t, err)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	// Prevent automatic redirect following
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// ---- Integration Tests ----

func TestIntegration_CreateAndGet(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	// Create
	body := `{"originalUrl":"https://integration-test.example.com/path"}`
	resp := apiPost(t, server, "/v1/urls", body, testAPIKey)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created model.CreateURLResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "https://integration-test.example.com/path", created.OriginalURL)

	// Get
	getResp := apiGet(t, server, "/v1/urls/"+created.ID, testAPIKey)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var detail model.URLDetailsResponse
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&detail))
	assert.Equal(t, created.ID, detail.ID)
}

func TestIntegration_AuthRequired(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	body := `{"originalUrl":"https://example.com"}`
	resp := apiPost(t, server, "/v1/urls", body, "") // no API key
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestIntegration_Redirect(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	body := `{"originalUrl":"https://redirect-target.example.com","customAlias":"redir-int"}`
	createResp := apiPost(t, server, "/v1/urls", body, testAPIKey)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	createResp.Body.Close()

	redirectResp := apiGet(t, server, "/redir-int", "")
	defer redirectResp.Body.Close()
	assert.Equal(t, http.StatusMovedPermanently, redirectResp.StatusCode)
	assert.Equal(t, "https://redirect-target.example.com", redirectResp.Header.Get("Location"))
}

func TestIntegration_Redirect_NotFound(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	resp := apiGet(t, server, "/no-such-id", "")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestIntegration_Redirect_Expired(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"originalUrl":"https://example.com","customAlias":"exp-int","expirationDate":"%s"}`, past)
	createResp := apiPost(t, server, "/v1/urls", body, testAPIKey)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	createResp.Body.Close()

	resp := apiGet(t, server, "/exp-int", "")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusGone, resp.StatusCode)
}

func TestIntegration_AliasConflict(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	body := `{"originalUrl":"https://example.com","customAlias":"alias-conflict"}`
	r1 := apiPost(t, server, "/v1/urls", body, testAPIKey)
	r1.Body.Close()
	require.Equal(t, http.StatusCreated, r1.StatusCode)

	r2 := apiPost(t, server, "/v1/urls", body, testAPIKey)
	defer r2.Body.Close()
	assert.Equal(t, http.StatusConflict, r2.StatusCode)
}

func TestIntegration_ListURLs(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	for i := range 4 {
		body := fmt.Sprintf(`{"originalUrl":"https://list-test.example.com/%d"}`, i)
		r := apiPost(t, server, "/v1/urls", body, testAPIKey)
		r.Body.Close()
	}

	listResp := apiGet(t, server, "/v1/urls?page=1&size=3", testAPIKey)
	defer listResp.Body.Close()
	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	var list model.ListURLsResponse
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	assert.Equal(t, int64(4), list.Total)
	assert.Len(t, list.Data, 3)
	assert.Equal(t, 1, list.Page)
	assert.Equal(t, 3, list.Size)
}

func TestIntegration_ClickCount(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	body := `{"originalUrl":"https://click-count.example.com","customAlias":"clk-int"}`
	createResp := apiPost(t, server, "/v1/urls", body, testAPIKey)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	createResp.Body.Close()

	// Trigger 4 redirects
	for range 4 {
		r := apiGet(t, server, "/clk-int", "")
		r.Body.Close()
	}

	detailResp := apiGet(t, server, "/v1/urls/clk-int", testAPIKey)
	defer detailResp.Body.Close()

	var detail model.URLDetailsResponse
	require.NoError(t, json.NewDecoder(detailResp.Body).Decode(&detail))
	assert.Equal(t, int64(4), detail.ClickCount)
}

func TestIntegration_InvalidURL_Returns400(t *testing.T) {
	server := buildTestServer(t)
	defer server.Close()

	body := `{"originalUrl":"ftp://bad-scheme.example.com"}`
	resp := apiPost(t, server, "/v1/urls", body, testAPIKey)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errResp model.ErrorResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errResp))
	assert.Equal(t, "INVALID_URL", errResp.Error.Code)
}
