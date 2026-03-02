package api

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/textfmt"
)

func (s Server) handleScheduleHTML(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	uiLang := preferredUILanguage(r.Header.Get("Accept-Language"))
	uiLoc, uiTimezone, timezoneConfigured, err := s.resolveUILocation(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	nowLocal := time.Now().In(uiLoc)
	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if view == "" {
		view = "calendar"
	}
	if view != "calendar" && view != "publications" && view != "drafts" && view != "create" && view != "failed" && view != "settings" {
		view = "calendar"
	}
	editID := strings.TrimSpace(r.URL.Query().Get("edit_id"))
	returnTo := strings.TrimSpace(r.URL.Query().Get("return_to"))
	createError := strings.TrimSpace(r.URL.Query().Get("error"))
	createSuccess := strings.TrimSpace(r.URL.Query().Get("success"))
	failedError := strings.TrimSpace(r.URL.Query().Get("failed_error"))
	failedSuccess := strings.TrimSpace(r.URL.Query().Get("failed_success"))
	settingsError := strings.TrimSpace(r.URL.Query().Get("tz_error"))
	settingsSuccess := strings.TrimSpace(r.URL.Query().Get("tz_success"))
	accountsError := strings.TrimSpace(r.URL.Query().Get("accounts_error"))
	accountsSuccess := strings.TrimSpace(r.URL.Query().Get("accounts_success"))
	mediaError := strings.TrimSpace(r.URL.Query().Get("media_error"))
	mediaSuccess := strings.TrimSpace(r.URL.Query().Get("media_success"))
	displayMonth := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, uiLoc)
	if monthRaw := strings.TrimSpace(r.URL.Query().Get("month")); monthRaw != "" {
		if parsedMonth, err := time.ParseInLocation("2006-01", monthRaw, uiLoc); err == nil {
			displayMonth = time.Date(parsedMonth.Year(), parsedMonth.Month(), 1, 0, 0, 0, 0, uiLoc)
		}
	}
	monthStartLocal := displayMonth
	monthEndLocal := monthStartLocal.AddDate(0, 1, 0).Add(-time.Second)
	from := monthStartLocal.UTC()
	to := monthEndLocal.UTC()
	items, err := s.Store.ListSchedule(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for i := range items {
		if !items[i].ScheduledAt.IsZero() {
			items[i].ScheduledAt = items[i].ScheduledAt.In(uiLoc)
		}
	}
	publicationsWindowDays := 14
	publicationsFrom := nowLocal
	publicationsTo := nowLocal.AddDate(0, 0, publicationsWindowDays)
	publicationsRaw, err := s.Store.ListSchedule(r.Context(), publicationsFrom.UTC(), publicationsTo.UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	publicationsItems := make([]domain.Post, 0, len(publicationsRaw))
	for _, item := range publicationsRaw {
		if item.Status != domain.PostStatusScheduled {
			continue
		}
		if !item.ScheduledAt.IsZero() {
			item.ScheduledAt = item.ScheduledAt.In(uiLoc)
		}
		publicationsItems = append(publicationsItems, item)
	}
	drafts, err := s.Store.ListDrafts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	accounts, err := s.Store.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	connectedAccounts := make([]domain.SocialAccount, 0, len(accounts))
	for _, account := range accounts {
		if account.Status == domain.AccountStatusConnected {
			connectedAccounts = append(connectedAccounts, account)
		}
	}
	deadLetters, err := s.Store.ListDeadLetters(r.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	scheduledCount := len(publicationsItems)
	failedCount := len(deadLetters)
	var nextRun *time.Time
	for _, item := range publicationsItems {
		if !item.ScheduledAt.IsZero() && (nextRun == nil || item.ScheduledAt.Before(*nextRun)) {
			t := item.ScheduledAt
			nextRun = &t
		}
	}
	nextRunLabel := uiMessage(uiLang, "stats.next_run.none")
	if nextRun != nil {
		nextRunLabel = nextRun.In(uiLoc).Format("2006-01-02 15:04 MST")
	}
	settingsAccounts := make([]settingsAccountItem, 0, len(accounts))
	for _, account := range accounts {
		lastError := ""
		if account.LastError != nil {
			lastError = strings.TrimSpace(*account.LastError)
		}
		statusClass := "status-disconnected"
		statusLabel := strings.TrimSpace(string(account.Status))
		switch account.Status {
		case domain.AccountStatusConnected:
			statusClass = "status-connected"
			statusLabel = uiMessage(uiLang, "settings.status.connected")
		case domain.AccountStatusDisconnected:
			statusLabel = uiMessage(uiLang, "settings.status.disconnected")
		case domain.AccountStatusError:
			statusClass = "status-error"
			statusLabel = uiMessage(uiLang, "settings.status.error")
		}
		if statusLabel == "" {
			statusLabel = strings.ToUpper(strings.TrimSpace(string(account.Status)))
		}
		settingsAccounts = append(settingsAccounts, settingsAccountItem{
			ID:          account.ID,
			DisplayName: account.DisplayName,
			Platform:    account.Platform,
			XPremium:    account.XPremium,
			AuthMethod:  account.AuthMethod,
			Status:      account.Status,
			StatusClass: statusClass,
			StatusLabel: statusLabel,
			LastError:   lastError,
		})
	}

	firstWeekday := int(monthStartLocal.Weekday())
	firstWeekday = (firstWeekday + 6) % 7
	gridStart := monthStartLocal.AddDate(0, 0, -firstWeekday)

	lastDayLocal := monthEndLocal
	lastWeekday := int(lastDayLocal.Weekday())
	lastWeekday = (lastWeekday + 6) % 7
	gridEnd := lastDayLocal.AddDate(0, 0, 6-lastWeekday)

	eventsByDate := make(map[string][]calendarEvent)
	detailsByDate := make(map[string][]dayDetailItem)
	for _, item := range items {
		if item.ScheduledAt.IsZero() {
			continue
		}
		localTime := item.ScheduledAt.In(uiLoc)
		key := localTime.Format("2006-01-02")
		statusClass := "drft"
		statusLabel := "DRFT"
		statusKey := "draft"
		switch item.Status {
		case domain.PostStatusPublished:
			statusClass = "live"
			statusLabel = "LIVE"
			statusKey = "published"
		case domain.PostStatusScheduled:
			statusClass = "schd"
			statusLabel = "SCHD"
			statusKey = "scheduled"
		}
		text := strings.TrimSpace(item.Text)
		if len(text) > 56 {
			text = text[:53] + "..."
		}
		eventsByDate[key] = append(eventsByDate[key], calendarEvent{
			TimeLabel:   localTime.Format("15:04"),
			StatusClass: statusClass,
			StatusLabel: statusLabel,
			StatusKey:   statusKey,
			TextPreview: text,
			Platform:    item.Platform,
		})
		detailsByDate[key] = append(detailsByDate[key], dayDetailItem{
			PostID:      item.ID,
			Editable:    canEditPostStatus(item.Status),
			Deletable:   canDeletePostStatus(item.Status),
			TimeLabel:   localTime.Format("15:04"),
			StatusClass: statusClass,
			StatusLabel: statusLabel,
			StatusKey:   statusKey,
			Text:        strings.TrimSpace(item.Text),
			Platform:    item.Platform,
			MediaCount:  len(item.Media),
		})
	}

	selectedDayLocal := nowLocal
	if selectedDayLocal.Month() != monthStartLocal.Month() || selectedDayLocal.Year() != monthStartLocal.Year() {
		selectedDayLocal = monthStartLocal
	}
	if dayRaw := strings.TrimSpace(r.URL.Query().Get("day")); dayRaw != "" {
		if parsedDay, err := time.ParseInLocation("2006-01-02", dayRaw, uiLoc); err == nil {
			selectedDayLocal = parsedDay
		}
	}

	var calendarDays []calendarDay
	for d := gridStart; !d.After(gridEnd); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		dayEvents := eventsByDate[key]
		calendarDays = append(calendarDays, calendarDay{
			DateKey:        key,
			DayNumber:      d.Day(),
			InCurrentMonth: d.Month() == monthStartLocal.Month(),
			IsToday:        d.Year() == nowLocal.Year() && d.Month() == nowLocal.Month() && d.Day() == nowLocal.Day(),
			IsSelected:     d.Year() == selectedDayLocal.Year() && d.Month() == selectedDayLocal.Month() && d.Day() == selectedDayLocal.Day(),
			Events:         dayEvents,
		})
	}

	var calendarWeeks [][]calendarDay
	for i := 0; i < len(calendarDays); i += 7 {
		end := i + 7
		if end > len(calendarDays) {
			end = len(calendarDays)
		}
		calendarWeeks = append(calendarWeeks, calendarDays[i:end])
	}

	calendarMonthLabel := localizedCalendarMonthLabel(monthStartLocal, uiLang)
	prevMonthParam := monthStartLocal.AddDate(0, -1, 0).Format("2006-01")
	nextMonthParam := monthStartLocal.AddDate(0, 1, 0).Format("2006-01")
	currentMonthParam := monthStartLocal.Format("2006-01")
	selectedDayKey := selectedDayLocal.Format("2006-01-02")
	selectedDayLabel := localizedSelectedDayLabel(selectedDayLocal, uiLang)
	selectedDayItems := detailsByDate[selectedDayKey]
	selectedDayPendingItems := make([]dayDetailItem, 0, len(selectedDayItems))
	selectedDayPublishedItems := make([]dayDetailItem, 0, len(selectedDayItems))
	for _, item := range selectedDayItems {
		if item.StatusKey == "published" {
			selectedDayPublishedItems = append(selectedDayPublishedItems, item)
			continue
		}
		selectedDayPendingItems = append(selectedDayPendingItems, item)
	}
	todayMonthParam := nowLocal.Format("2006-01")
	todayDayKey := nowLocal.Format("2006-01-02")
	currentViewURL := "/?view=calendar&month=" + currentMonthParam + "&day=" + selectedDayKey
	switch view {
	case "publications":
		currentViewURL = "/?view=publications"
	case "calendar":
		currentViewURL = "/?view=calendar&month=" + currentMonthParam + "&day=" + selectedDayKey
	case "drafts":
		currentViewURL = "/?view=drafts"
	case "failed":
		currentViewURL = "/?view=failed"
	case "settings":
		currentViewURL = "/?view=settings"
	case "create":
		if returnTo != "" {
			currentViewURL = returnTo
		}
	}
	createViewURL := "/?view=create&return_to=" + url.QueryEscape(currentViewURL)
	backURL := "/?view=calendar&month=" + currentMonthParam + "&day=" + selectedDayKey
	if returnTo != "" {
		backURL = returnTo
	}
	activeNavView := view
	if activeNavView == "create" {
		activeNavView = "calendar"
		if returnTo != "" {
			if parsed, err := url.Parse(returnTo); err == nil {
				sourceView := strings.ToLower(strings.TrimSpace(parsed.Query().Get("view")))
				switch sourceView {
				case "publications", "calendar", "drafts", "failed", "settings":
					activeNavView = sourceView
				}
			}
		}
	}
	failedItems := make([]failedQueueItem, 0, len(deadLetters))
	for _, dead := range deadLetters {
		post, err := s.Store.GetPost(r.Context(), dead.PostID)
		if err != nil {
			continue
		}
		scheduledAtLabel := uiMessage(uiLang, "common.no_date")
		if !post.ScheduledAt.IsZero() {
			scheduledAtLabel = post.ScheduledAt.In(uiLoc).Format("2006-01-02 15:04 MST")
		}
		failedAtLabel := dead.AttemptedAt.In(uiLoc).Format("2006-01-02 15:04 MST")
		failedItems = append(failedItems, failedQueueItem{
			DeadLetterID:     dead.ID,
			PostID:           post.ID,
			Text:             strings.TrimSpace(post.Text),
			Platform:         post.Platform,
			MediaCount:       len(post.Media),
			Attempts:         post.Attempts,
			MaxAttempts:      post.MaxAttempts,
			LastError:        strings.TrimSpace(dead.LastError),
			FailedAtLabel:    failedAtLabel,
			ScheduledAtLabel: scheduledAtLabel,
		})
	}
	mediaLibrary, err := s.listMediaItems(r.Context(), 200, uiLoc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	createRecentMedia := mediaLibrary
	if len(createRecentMedia) > 18 {
		createRecentMedia = createRecentMedia[:18]
	}
	var mediaInUseCount int
	var mediaTotalBytes int64
	for _, item := range mediaLibrary {
		mediaTotalBytes += item.SizeBytes
		if item.InUse {
			mediaInUseCount++
		}
	}
	mediaTotalSizeLabel := formatByteSize(mediaTotalBytes)
	var editingPost *domain.Post
	var createText string
	var createScheduledLocal string
	var createAccountID string
	if editID != "" {
		p, err := s.Store.GetPost(r.Context(), editID)
		if err == nil {
			editingPost = &p
			createText = p.Text
			createAccountID = strings.TrimSpace(p.AccountID)
			if !p.ScheduledAt.IsZero() {
				createScheduledLocal = p.ScheduledAt.In(uiLoc).Format("2006-01-02T15:04")
			}
		}
	}
	if qAccount := strings.TrimSpace(r.URL.Query().Get("account_id")); qAccount != "" {
		createAccountID = qAccount
	}
	if createAccountID == "" && len(connectedAccounts) > 0 {
		createAccountID = connectedAccounts[0].ID
	}
	if view == "create" && len(connectedAccounts) == 0 && createError == "" {
		createError = uiMessage(uiLang, "create.no_connected_accounts")
	}
	if qText := strings.TrimSpace(r.URL.Query().Get("text")); qText != "" {
		createText = qText
	}
	if qScheduled := strings.TrimSpace(r.URL.Query().Get("scheduled_at_local")); qScheduled != "" {
		createScheduledLocal = qScheduled
	}
	weekdayLabels := weekdayHeaders(uiLang)
	datePickerMonthNames := datePickerMonthLabels(uiLang)
	datePickerWeekdayNames := datePickerWeekdayLabels(uiLang)
	mcpURL, mcpAuthHint, mcpConfigJSON, mcpClaudeCommand, mcpCodexCommand, mcpCodexConfigTOML := s.mcpSettingsInfo(r)
	tpl := scheduleHTMLTemplate
	t, err := template.New("schedule").Funcs(template.FuncMap{
		"previewMarkdown": func(raw string) template.HTML {
			return template.HTML(textfmt.MarkdownToPreviewHTML(raw))
		},
		"t": func(key string, args ...any) string {
			return uiMessage(uiLang, key, args...)
		},
		"toJSON": func(v any) template.JS {
			raw, err := json.Marshal(v)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(raw)
		},
	}).Parse(tpl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, pageData{
		Lang:                      uiLang,
		View:                      view,
		ActiveNavView:             activeNavView,
		UITimezone:                uiTimezone,
		TimezoneConfigured:        timezoneConfigured,
		MCPURL:                    mcpURL,
		MCPAuthHint:               mcpAuthHint,
		MCPConfigJSON:             mcpConfigJSON,
		MCPClaudeCommand:          mcpClaudeCommand,
		MCPCodexCommand:           mcpCodexCommand,
		MCPCodexConfigTOML:        mcpCodexConfigTOML,
		Items:                     items,
		Publications:              publicationsItems,
		Drafts:                    drafts,
		FailedItems:               failedItems,
		CurrentViewURL:            currentViewURL,
		CreateViewURL:             createViewURL,
		ReturnTo:                  returnTo,
		BackURL:                   backURL,
		Accounts:                  connectedAccounts,
		EditingPost:               editingPost,
		CreateAccountID:           createAccountID,
		CreateText:                createText,
		CreateScheduledLocal:      createScheduledLocal,
		CreateError:               createError,
		CreateSuccess:             createSuccess,
		FailedError:               failedError,
		FailedSuccess:             failedSuccess,
		SettingsError:             settingsError,
		SettingsSuccess:           settingsSuccess,
		AccountsError:             accountsError,
		AccountsSuccess:           accountsSuccess,
		MediaError:                mediaError,
		MediaSuccess:              mediaSuccess,
		TotalAccountCount:         len(accounts),
		ConnectedAccountCount:     len(connectedAccounts),
		ScheduledCount:            scheduledCount,
		PublicationsWindowDays:    publicationsWindowDays,
		DraftCount:                len(drafts),
		FailedCount:               failedCount,
		SettingsAccounts:          settingsAccounts,
		MediaLibrary:              mediaLibrary,
		CreateRecentMedia:         createRecentMedia,
		MediaInUseCount:           mediaInUseCount,
		MediaTotalSizeLabel:       mediaTotalSizeLabel,
		NextRunLabel:              nextRunLabel,
		CalendarMonthLabel:        calendarMonthLabel,
		WeekdayLabels:             weekdayLabels,
		CalendarWeeks:             calendarWeeks,
		PrevMonthParam:            prevMonthParam,
		NextMonthParam:            nextMonthParam,
		CurrentMonthParam:         currentMonthParam,
		TodayMonthParam:           todayMonthParam,
		TodayDayKey:               todayDayKey,
		SelectedDayKey:            selectedDayKey,
		SelectedDayLabel:          selectedDayLabel,
		SelectedDayItems:          selectedDayItems,
		SelectedDayPendingItems:   selectedDayPendingItems,
		SelectedDayPublishedItems: selectedDayPublishedItems,
		DatePickerMonthNames:      datePickerMonthNames,
		DatePickerWeekdayNames:    datePickerWeekdayNames,
	})
}
