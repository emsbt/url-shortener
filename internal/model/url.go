package model

import "time"

// URL represents a shortened URL entity stored in the database.
type URL struct {
	ID             string     `json:"id"`
	ShortURL       string     `json:"shortUrl"`
	OriginalURL    string     `json:"originalUrl"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpirationDate *time.Time `json:"expirationDate,omitempty"`
	ClickCount     int64      `json:"clickCount"`
}

// CreateURLRequest is the payload for POST /v1/urls.
type CreateURLRequest struct {
	OriginalURL    string     `json:"originalUrl"`
	ExpirationDate *time.Time `json:"expirationDate,omitempty"`
	CustomAlias    string     `json:"customAlias,omitempty"`
}

// CreateURLResponse is the response body for a successfully created short URL.
type CreateURLResponse struct {
	ID             string     `json:"id"`
	ShortURL       string     `json:"shortUrl"`
	OriginalURL    string     `json:"originalUrl"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpirationDate *time.Time `json:"expirationDate,omitempty"`
}

// URLDetailsResponse is the response body for GET /v1/urls/{id}.
type URLDetailsResponse struct {
	ID             string     `json:"id"`
	ShortURL       string     `json:"shortUrl"`
	OriginalURL    string     `json:"originalUrl"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpirationDate *time.Time `json:"expirationDate,omitempty"`
	ClickCount     int64      `json:"clickCount"`
}

// ListURLsResponse is the response body for GET /v1/urls.
type ListURLsResponse struct {
	Data  []URLDetailsResponse `json:"data"`
	Page  int                  `json:"page"`
	Size  int                  `json:"size"`
	Total int64                `json:"total"`
}

// ErrorResponse is the standard error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail holds the machine-readable code and human-readable message.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
