package api

import "github.com/antoniolg/publisher/internal/domain"

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
	TimeLabel   string
	StatusClass string
	StatusLabel string
	StatusKey   string
	TextPreview string
	Platform    domain.Platform
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

type calendarDay struct {
	DateKey        string
	DayNumber      int
	InCurrentMonth bool
	IsToday        bool
	IsSelected     bool
	Events         []calendarEvent
}

type createMediaAttachment struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	PreviewURL string `json:"previewUrl"`
}

type pageData struct {
	Lang                      string
	View                      string
	ActiveNavView             string
	UITimezone                string
	TimezoneConfigured        bool
	AppVersion                string
	MCPURL                    string
	MCPAuthHint               string
	MCPConfigJSON             string
	MCPClaudeCommand          string
	MCPCodexCommand           string
	MCPCodexConfigTOML        string
	Items                     []domain.Post
	Publications              []domain.Post
	Drafts                    []domain.Post
	FailedItems               []failedQueueItem
	CurrentViewURL            string
	CreateViewURL             string
	ReturnTo                  string
	BackURL                   string
	Accounts                  []domain.SocialAccount
	EditingPost               *domain.Post
	CreateInitialMedia        []createMediaAttachment
	CreateAccountID           string
	CreateText                string
	CreateScheduledLocal      string
	CreateError               string
	CreateSuccess             string
	FailedError               string
	FailedSuccess             string
	SettingsError             string
	SettingsSuccess           string
	AccountsError             string
	AccountsSuccess           string
	MediaError                string
	MediaSuccess              string
	TotalAccountCount         int
	ConnectedAccountCount     int
	ScheduledCount            int
	PublicationsWindowDays    int
	DraftCount                int
	FailedCount               int
	SettingsAccounts          []settingsAccountItem
	MediaLibrary              []mediaListItem
	CreateRecentMedia         []mediaListItem
	MediaInUseCount           int
	MediaTotalSizeLabel       string
	NextRunLabel              string
	CalendarMonthLabel        string
	WeekdayLabels             []string
	CalendarWeeks             [][]calendarDay
	PrevMonthParam            string
	NextMonthParam            string
	CurrentMonthParam         string
	TodayMonthParam           string
	TodayDayKey               string
	SelectedDayKey            string
	SelectedDayLabel          string
	SelectedDayItems          []dayDetailItem
	SelectedDayPendingItems   []dayDetailItem
	SelectedDayPublishedItems []dayDetailItem
	DatePickerMonthNames      []string
	DatePickerWeekdayNames    []string
}
