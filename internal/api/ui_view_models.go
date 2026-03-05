package api

import (
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type settingsAccountItem struct {
	ID          string
	DisplayName string
	Platform    domain.Platform
	XPremium    bool
	AuthMethod  domain.AuthMethod
	Status      domain.AccountStatus
	StatusClass string
	StatusLabel string
	LastError   string
}

type calendarEvent struct {
	TimeLabel     string
	StatusClass   string
	StatusLabel   string
	StatusKey     string
	TextPreview   string
	ThreadLabel   string
	Platform      domain.Platform
	Platforms     []domain.Platform
	PostCount     int
	MultiPlatform bool
}

type dayDetailItem struct {
	PostID      string
	Editable    bool
	Deletable   bool
	TimeLabel   string
	StatusClass string
	StatusLabel string
	StatusKey   string
	Text        string
	ThreadLabel string
	Platform    domain.Platform
	MediaCount  int
}

type failedQueueItem struct {
	DeadLetterID     string
	PostID           string
	Text             string
	Platform         domain.Platform
	MediaCount       int
	Attempts         int
	MaxAttempts      int
	LastError        string
	FailedAtLabel    string
	ScheduledAtLabel string
}

type publicationGroupItem struct {
	PrimaryPostID   string
	PostIDs         []string
	PrimaryPlatform domain.Platform
	MultiPlatform   bool
	Platforms       []domain.Platform
	AccountIDs      []string
	PostCount       int
	ScheduledAt     time.Time
	Text            string
	ThreadLabel     string
	MediaCount      int
	EditURL         string
}

type failedGroupItem struct {
	PrimaryPostID    string
	DeadLetterIDs    []string
	DeadLetterIDsCSV string
	Platforms        []domain.Platform
	AccountIDs       []string
	PostCount        int
	ScheduledAtLabel string
	Text             string
	ThreadLabel      string
	MediaCount       int
	Attempts         int
	MaxAttempts      int
	FailedAtLabel    string
	LastError        string
	EditURL          string
}

type calendarDay struct {
	DateKey        string
	DayNumber      int
	InCurrentMonth bool
	IsToday        bool
	IsSelected     bool
	EventCount     int
	Events         []calendarEvent
}

type createMediaAttachment struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	PreviewURL string `json:"previewUrl"`
}

type createThreadSegment struct {
	Text  string                  `json:"text"`
	Media []createMediaAttachment `json:"media"`
}

type pageData struct {
	Lang                       string
	View                       string
	ActiveNavView              string
	UITimezone                 string
	TimezoneConfigured         bool
	AppVersion                 string
	MCPURL                     string
	MCPAuthHint                string
	MCPConfigJSON              string
	MCPClaudeCommand           string
	MCPCodexCommand            string
	MCPCodexConfigTOML         string
	Items                      []domain.Post
	Publications               []domain.Post
	PublicationGroups          []publicationGroupItem
	Drafts                     []domain.Post
	DraftGroups                []publicationGroupItem
	FailedItems                []failedQueueItem
	FailedGroups               []failedGroupItem
	CurrentViewURL             string
	CreateViewURL              string
	ReturnTo                   string
	BackURL                    string
	Accounts                   []domain.SocialAccount
	EditingPost                *domain.Post
	CreateInitialMedia         []createMediaAttachment
	CreateInitialSegments      []createThreadSegment
	CreateAccountID            string
	CreateAccountIDs           []string
	CreateText                 string
	CreateScheduledLocal       string
	CreateError                string
	CreateSuccess              string
	FailedError                string
	FailedSuccess              string
	SettingsError              string
	SettingsSuccess            string
	AccountsError              string
	AccountsSuccess            string
	MediaError                 string
	MediaSuccess               string
	TotalAccountCount          int
	ConnectedAccountCount      int
	ScheduledCount             int
	PublicationsWindowDays     int
	DraftCount                 int
	FailedCount                int
	SettingsAccounts           []settingsAccountItem
	MediaLibrary               []mediaListItem
	CreateRecentMedia          []mediaListItem
	MediaInUseCount            int
	MediaTotalSizeLabel        string
	NextRunLabel               string
	CalendarMonthLabel         string
	WeekdayLabels              []string
	CalendarWeeks              [][]calendarDay
	PrevMonthParam             string
	NextMonthParam             string
	CurrentMonthParam          string
	TodayMonthParam            string
	TodayDayKey                string
	SelectedDayKey             string
	SelectedDayLabel           string
	SelectedDayItems           []dayDetailItem
	SelectedDayPendingItems    []dayDetailItem
	SelectedDayPublishedItems  []dayDetailItem
	SelectedDayPendingGroups   []publicationGroupItem
	SelectedDayPublishedGroups []publicationGroupItem
	SelectedDayPendingCount    int
	SelectedDayPublishedCount  int
	DatePickerMonthNames       []string
	DatePickerWeekdayNames     []string
}
