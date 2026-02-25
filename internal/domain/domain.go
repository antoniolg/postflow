package domain

import "time"

type Platform string

const (
	PlatformX Platform = "x"
)

type PostStatus string

const (
	PostStatusDraft      PostStatus = "draft"
	PostStatusScheduled  PostStatus = "scheduled"
	PostStatusPublishing PostStatus = "publishing"
	PostStatusPublished  PostStatus = "published"
	PostStatusFailed     PostStatus = "failed"
	PostStatusCanceled   PostStatus = "canceled"
)

type Media struct {
	ID           string    `json:"id"`
	Platform     Platform  `json:"platform"`
	Kind         string    `json:"kind"`
	OriginalName string    `json:"original_name"`
	StoragePath  string    `json:"storage_path"`
	MimeType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	CreatedAt    time.Time `json:"created_at"`
}

type Post struct {
	ID             string     `json:"id"`
	Platform       Platform   `json:"platform"`
	Text           string     `json:"text"`
	Status         PostStatus `json:"status"`
	ScheduledAt    time.Time  `json:"scheduled_at"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	ExternalID     *string    `json:"external_id,omitempty"`
	Error          *string    `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Media          []Media    `json:"media,omitempty"`
}

type DeadLetter struct {
	ID          string    `json:"id"`
	PostID      string    `json:"post_id"`
	Reason      string    `json:"reason"`
	LastError   string    `json:"last_error"`
	AttemptedAt time.Time `json:"attempted_at"`
}
