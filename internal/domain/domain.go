package domain

import "time"

type Platform string

const (
	PlatformX Platform = "x"
)

type PostStatus string

const (
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
	ID          string     `json:"id"`
	Platform    Platform   `json:"platform"`
	Text        string     `json:"text"`
	Status      PostStatus `json:"status"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	ExternalID  *string    `json:"external_id,omitempty"`
	Error       *string    `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Media       []Media    `json:"media,omitempty"`
}
