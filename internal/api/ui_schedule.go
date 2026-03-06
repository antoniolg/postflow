package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/textfmt"
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
	threadRoots := make(map[string]struct{})
	collectThreadRoot := func(post domain.Post) {
		rootID := postThreadRootID(post)
		if rootID == "" {
			return
		}
		threadRoots[rootID] = struct{}{}
	}
	for _, item := range items {
		collectThreadRoot(item)
	}
	for _, item := range publicationsItems {
		collectThreadRoot(item)
	}
	for _, item := range drafts {
		collectThreadRoot(item)
	}
	threadTotals := make(map[string]int, len(threadRoots))
	for rootID := range threadRoots {
		total := 1
		threadPosts, err := s.Store.ListThreadPosts(r.Context(), rootID)
		if err == nil && len(threadPosts) > 0 {
			maxPos := 1
			for _, post := range threadPosts {
				if post.ThreadPosition > maxPos {
					maxPos = post.ThreadPosition
				}
			}
			if len(threadPosts) > maxPos {
				maxPos = len(threadPosts)
			}
			total = maxPos
		}
		threadTotals[rootID] = total
	}
	threadLabelFor := func(post domain.Post) string {
		return formatThreadLabel(post, threadTotals)
	}
	publicationGroups := groupPublicationsByContent(publicationsItems, threadLabelFor)
	draftGroups := groupPublicationsByContent(drafts, threadLabelFor)
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
	var nextRun *time.Time
	for _, item := range publicationsItems {
		if !item.ScheduledAt.IsZero() && (nextRun == nil || item.ScheduledAt.Before(*nextRun)) {
			t := item.ScheduledAt
			nextRun = &t
		}
	}
	nextRunLabel := uiMessage(uiLang, "stats.next_run.none")
	if nextRun != nil {
		nextRunLabel = nextRun.In(uiLoc).Format("2006-01-02 15:04")
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

	postsByDate := make(map[string][]domain.Post)
	for _, item := range items {
		if item.ScheduledAt.IsZero() {
			continue
		}
		localTime := item.ScheduledAt.In(uiLoc)
		key := localTime.Format("2006-01-02")
		postsByDate[key] = append(postsByDate[key], item)
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
		dayPosts := postsByDate[key]
		dayEvents := groupCalendarEventsByContent(dayPosts, threadLabelFor)
		calendarDays = append(calendarDays, calendarDay{
			DateKey:        key,
			DayNumber:      d.Day(),
			InCurrentMonth: d.Month() == monthStartLocal.Month(),
			IsToday:        d.Year() == nowLocal.Year() && d.Month() == nowLocal.Month() && d.Day() == nowLocal.Day(),
			IsSelected:     d.Year() == selectedDayLocal.Year() && d.Month() == selectedDayLocal.Month() && d.Day() == selectedDayLocal.Day(),
			EventCount:     len(dayPosts),
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
	selectedDayPosts := postsByDate[selectedDayKey]
	selectedDayPendingPosts := make([]domain.Post, 0, len(selectedDayPosts))
	selectedDayPublishedPosts := make([]domain.Post, 0, len(selectedDayPosts))
	selectedDayFailedPostIDs := make(map[string]struct{}, len(selectedDayPosts))
	for _, item := range selectedDayPosts {
		switch item.Status {
		case domain.PostStatusPublished:
			selectedDayPublishedPosts = append(selectedDayPublishedPosts, item)
		case domain.PostStatusFailed:
			selectedDayFailedPostIDs[item.ID] = struct{}{}
		default:
			selectedDayPendingPosts = append(selectedDayPendingPosts, item)
		}
	}
	selectedDayPendingGroups := groupPublicationsByContent(selectedDayPendingPosts, threadLabelFor)
	selectedDayPublishedGroups := groupPublicationsByContent(selectedDayPublishedPosts, threadLabelFor)
	selectedDayPendingCount := len(selectedDayPendingPosts)
	selectedDayPublishedCount := len(selectedDayPublishedPosts)
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
	if view == "calendar" {
		createViewURL += "&calendar_day=" + url.QueryEscape(selectedDayKey)
	}
	for i := range publicationGroups {
		publicationGroups[i].EditURL = publicationGroupEditURL(publicationGroups[i], currentViewURL)
	}
	for i := range draftGroups {
		draftGroups[i].EditURL = publicationGroupEditURL(draftGroups[i], currentViewURL)
	}
	for i := range selectedDayPendingGroups {
		selectedDayPendingGroups[i].EditURL = publicationGroupEditURL(selectedDayPendingGroups[i], currentViewURL)
	}
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
	failedEnvelopes := make([]failedPostEnvelope, 0, len(deadLetters))
	for _, dead := range deadLetters {
		post, err := s.Store.GetPost(r.Context(), dead.PostID)
		if err != nil {
			continue
		}
		scheduledAtLabel := uiMessage(uiLang, "common.no_date")
		if !post.ScheduledAt.IsZero() {
			scheduledAtLabel = post.ScheduledAt.In(uiLoc).Format("2006-01-02 15:04")
		}
		failedAtLabel := dead.AttemptedAt.In(uiLoc).Format("2006-01-02 15:04")
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
		failedEnvelopes = append(failedEnvelopes, failedPostEnvelope{
			Post:         post,
			DeadLetterID: dead.ID,
			FailedAt:     dead.AttemptedAt,
			LastError:    dead.LastError,
		})
	}
	failedGroups := groupFailedPosts(failedEnvelopes, threadLabelFor, uiLoc, uiMessage(uiLang, "common.no_date"))
	for i := range failedGroups {
		failedGroups[i].EditURL = publicationGroupEditURL(publicationGroupItem{
			PrimaryPostID: failedGroups[i].PrimaryPostID,
			AccountIDs:    append([]string(nil), failedGroups[i].AccountIDs...),
		}, currentViewURL)
	}
	selectedDayFailedEnvelopes := make([]failedPostEnvelope, 0, len(selectedDayFailedPostIDs))
	for _, item := range failedEnvelopes {
		if _, ok := selectedDayFailedPostIDs[item.Post.ID]; ok {
			selectedDayFailedEnvelopes = append(selectedDayFailedEnvelopes, item)
		}
	}
	selectedDayFailedGroups := groupFailedPosts(selectedDayFailedEnvelopes, threadLabelFor, uiLoc, uiMessage(uiLang, "common.no_date"))
	for i := range selectedDayFailedGroups {
		selectedDayFailedGroups[i].EditURL = publicationGroupEditURL(publicationGroupItem{
			PrimaryPostID: selectedDayFailedGroups[i].PrimaryPostID,
			AccountIDs:    append([]string(nil), selectedDayFailedGroups[i].AccountIDs...),
		}, currentViewURL)
	}
	selectedDayFailedCount := len(selectedDayFailedEnvelopes)
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
	createInitialMedia := make([]createMediaAttachment, 0)
	createInitialSegments := make([]createThreadSegment, 0, 1)
	var createText string
	var createScheduledLocal string
	var createAccountID string
	createAccountIDs := make([]string, 0, 2)
	if editID != "" {
		p, err := s.Store.GetPost(r.Context(), editID)
		if err == nil {
			rootID := postThreadRootID(p)
			threadPosts, listErr := s.Store.ListThreadPosts(r.Context(), rootID)
			if listErr == nil && len(threadPosts) > 0 {
				sort.SliceStable(threadPosts, func(i, j int) bool {
					leftPos := threadPosts[i].ThreadPosition
					rightPos := threadPosts[j].ThreadPosition
					if leftPos <= 0 {
						leftPos = 1
					}
					if rightPos <= 0 {
						rightPos = 1
					}
					if leftPos != rightPos {
						return leftPos < rightPos
					}
					return threadPosts[i].CreatedAt.Before(threadPosts[j].CreatedAt)
				})
				rootPost := threadPosts[0]
				editingPost = &rootPost
				createText = rootPost.Text
				createAccountID = strings.TrimSpace(rootPost.AccountID)
				if !rootPost.ScheduledAt.IsZero() {
					createScheduledLocal = rootPost.ScheduledAt.In(uiLoc).Format("2006-01-02T15:04")
				}
				for _, step := range threadPosts {
					attachments := mediaAttachmentsFromPost(step)
					createInitialSegments = append(createInitialSegments, createThreadSegment{
						Text:  strings.TrimSpace(step.Text),
						Media: attachments,
					})
				}
				if len(createInitialSegments) > 0 {
					createInitialMedia = append(createInitialMedia, createInitialSegments[0].Media...)
				}
			}
			if len(createInitialSegments) == 0 {
				editingPost = &p
				createText = p.Text
				createAccountID = strings.TrimSpace(p.AccountID)
				if !p.ScheduledAt.IsZero() {
					createScheduledLocal = p.ScheduledAt.In(uiLoc).Format("2006-01-02T15:04")
				}
				createInitialMedia = mediaAttachmentsFromPost(p)
				createInitialSegments = append(createInitialSegments, createThreadSegment{
					Text:  strings.TrimSpace(p.Text),
					Media: append([]createMediaAttachment(nil), createInitialMedia...),
				})
			}
		}
	}
	if len(createInitialSegments) == 0 {
		createInitialSegments = append(createInitialSegments, createThreadSegment{
			Text:  strings.TrimSpace(createText),
			Media: append([]createMediaAttachment(nil), createInitialMedia...),
		})
	}
	if qAccount := strings.TrimSpace(r.URL.Query().Get("account_id")); qAccount != "" {
		createAccountID = qAccount
	}
	if qAccountIDs := normalizeAccountIDListCSV(r.URL.Query().Get("account_ids")); len(qAccountIDs) > 0 {
		createAccountIDs = qAccountIDs
	}
	if createAccountID != "" {
		createAccountIDs = prependAccountID(createAccountIDs, createAccountID)
	}
	createAccountIDs = filterAccountIDsByConnectedAccounts(createAccountIDs, connectedAccounts)
	if len(createAccountIDs) > 0 {
		createAccountID = createAccountIDs[0]
	}
	if createAccountID == "" && len(connectedAccounts) > 0 {
		createAccountID = connectedAccounts[0].ID
	}
	if len(createAccountIDs) == 0 && createAccountID != "" {
		createAccountIDs = []string{createAccountID}
	}
	if view == "create" && len(connectedAccounts) == 0 && createError == "" {
		createError = uiMessage(uiLang, "create.no_connected_accounts")
	}
	if qText := strings.TrimSpace(r.URL.Query().Get("text")); qText != "" {
		createText = qText
	}
	if qScheduled := strings.TrimSpace(r.URL.Query().Get("scheduled_at_local")); qScheduled != "" {
		createScheduledLocal = qScheduled
	} else if view == "create" && editID == "" {
		if calendarDay, ok := calendarSelectedDayFromCreateQuery(r.URL.Query(), uiLoc); ok {
			createScheduledLocal = defaultCalendarCreateScheduledLocal(calendarDay, nowLocal)
		}
	}
	if len(createInitialSegments) == 0 {
		createInitialSegments = append(createInitialSegments, createThreadSegment{})
	}
	createInitialSegments[0].Text = strings.TrimSpace(createText)
	createInitialSegments[0].Media = append([]createMediaAttachment(nil), createInitialMedia...)

	weekdayLabels := weekdayHeaders(uiLang)
	datePickerMonthNames := datePickerMonthLabels(uiLang)
	datePickerWeekdayNames := datePickerWeekdayLabels(uiLang)
	mcpURL, mcpAuthHint, mcpConfigJSON, mcpClaudeCommand, mcpCodexCommand, mcpCodexConfigTOML := s.mcpSettingsInfo(r)
	appVersion := s.appVersion()
	tpl := scheduleHTMLTemplate
	selectedCreateAccountIDs := make(map[string]struct{}, len(createAccountIDs))
	for _, id := range createAccountIDs {
		selectedCreateAccountIDs[strings.TrimSpace(id)] = struct{}{}
	}
	t, err := template.New("schedule").Funcs(template.FuncMap{
		"previewMarkdown": func(raw string) template.HTML {
			return template.HTML(textfmt.MarkdownToPreviewHTML(raw))
		},
		"t": func(key string, args ...any) string {
			return uiMessage(uiLang, key, args...)
		},
		"threadLabel": func(post domain.Post) string {
			return threadLabelFor(post)
		},
		"accountSelected": func(accountID string) bool {
			_, ok := selectedCreateAccountIDs[strings.TrimSpace(accountID)]
			return ok
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
		Lang:                       uiLang,
		View:                       view,
		ActiveNavView:              activeNavView,
		UITimezone:                 uiTimezone,
		TimezoneConfigured:         timezoneConfigured,
		AppVersion:                 appVersion,
		MCPURL:                     mcpURL,
		MCPAuthHint:                mcpAuthHint,
		MCPConfigJSON:              mcpConfigJSON,
		MCPClaudeCommand:           mcpClaudeCommand,
		MCPCodexCommand:            mcpCodexCommand,
		MCPCodexConfigTOML:         mcpCodexConfigTOML,
		Items:                      items,
		Publications:               publicationsItems,
		PublicationGroups:          publicationGroups,
		Drafts:                     drafts,
		DraftGroups:                draftGroups,
		FailedItems:                failedItems,
		FailedGroups:               failedGroups,
		CurrentViewURL:             currentViewURL,
		CreateViewURL:              createViewURL,
		ReturnTo:                   returnTo,
		BackURL:                    backURL,
		Accounts:                   connectedAccounts,
		EditingPost:                editingPost,
		CreateInitialMedia:         createInitialMedia,
		CreateInitialSegments:      createInitialSegments,
		CreateAccountID:            createAccountID,
		CreateAccountIDs:           createAccountIDs,
		CreateText:                 createText,
		CreateScheduledLocal:       createScheduledLocal,
		CreateError:                createError,
		CreateSuccess:              createSuccess,
		FailedError:                failedError,
		FailedSuccess:              failedSuccess,
		SettingsError:              settingsError,
		SettingsSuccess:            settingsSuccess,
		AccountsError:              accountsError,
		AccountsSuccess:            accountsSuccess,
		MediaError:                 mediaError,
		MediaSuccess:               mediaSuccess,
		TotalAccountCount:          len(accounts),
		ConnectedAccountCount:      len(connectedAccounts),
		ScheduledCount:             len(publicationGroups),
		PublicationsWindowDays:     publicationsWindowDays,
		DraftCount:                 len(draftGroups),
		FailedCount:                len(failedGroups),
		SettingsAccounts:           settingsAccounts,
		MediaLibrary:               mediaLibrary,
		CreateRecentMedia:          createRecentMedia,
		MediaInUseCount:            mediaInUseCount,
		MediaTotalSizeLabel:        mediaTotalSizeLabel,
		NextRunLabel:               nextRunLabel,
		CalendarMonthLabel:         calendarMonthLabel,
		WeekdayLabels:              weekdayLabels,
		CalendarWeeks:              calendarWeeks,
		PrevMonthParam:             prevMonthParam,
		NextMonthParam:             nextMonthParam,
		CurrentMonthParam:          currentMonthParam,
		TodayMonthParam:            todayMonthParam,
		TodayDayKey:                todayDayKey,
		SelectedDayKey:             selectedDayKey,
		SelectedDayLabel:           selectedDayLabel,
		SelectedDayItems:           nil,
		SelectedDayPendingItems:    nil,
		SelectedDayPublishedItems:  nil,
		SelectedDayPendingGroups:   selectedDayPendingGroups,
		SelectedDayPublishedGroups: selectedDayPublishedGroups,
		SelectedDayFailedGroups:    selectedDayFailedGroups,
		SelectedDayPendingCount:    selectedDayPendingCount,
		SelectedDayPublishedCount:  selectedDayPublishedCount,
		SelectedDayFailedCount:     selectedDayFailedCount,
		DatePickerMonthNames:       datePickerMonthNames,
		DatePickerWeekdayNames:     datePickerWeekdayNames,
	})
}

func mediaAttachmentsFromPost(post domain.Post) []createMediaAttachment {
	if len(post.Media) == 0 {
		return nil
	}
	attachments := make([]createMediaAttachment, 0, len(post.Media))
	for _, media := range post.Media {
		mediaID := strings.TrimSpace(media.ID)
		if mediaID == "" {
			continue
		}
		attachments = append(attachments, createMediaAttachment{
			ID:         mediaID,
			Name:       strings.TrimSpace(media.OriginalName),
			Size:       media.SizeBytes,
			Mime:       strings.TrimSpace(media.MimeType),
			PreviewURL: mediaContentURL(mediaID),
		})
	}
	return attachments
}

func postThreadRootID(post domain.Post) string {
	if post.RootPostID != nil {
		if root := strings.TrimSpace(*post.RootPostID); root != "" {
			return root
		}
	}
	return strings.TrimSpace(post.ID)
}

func formatThreadLabel(post domain.Post, totals map[string]int) string {
	rootID := postThreadRootID(post)
	if rootID == "" {
		return ""
	}
	total := totals[rootID]
	if total <= 1 {
		return ""
	}
	position := post.ThreadPosition
	if position <= 0 {
		position = 1
	}
	if position > total {
		total = position
	}
	return fmt.Sprintf("#%d/%d", position, total)
}

func normalizeAccountIDListCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func prependAccountID(ids []string, head string) []string {
	head = strings.TrimSpace(head)
	if head == "" {
		return ids
	}
	out := make([]string, 0, len(ids)+1)
	out = append(out, head)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || id == head {
			continue
		}
		out = append(out, id)
	}
	return out
}

func filterAccountIDsByConnectedAccounts(ids []string, accounts []domain.SocialAccount) []string {
	if len(ids) == 0 || len(accounts) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" {
			continue
		}
		allowed[id] = struct{}{}
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := allowed[id]; !exists {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func publicationGroupEditURL(group publicationGroupItem, currentViewURL string) string {
	values := url.Values{}
	values.Set("view", "create")
	values.Set("edit_id", strings.TrimSpace(group.PrimaryPostID))
	values.Set("return_to", currentViewURL)
	if len(group.AccountIDs) > 0 {
		values.Set("account_id", group.AccountIDs[0])
		values.Set("account_ids", strings.Join(group.AccountIDs, ","))
	}
	return "/?" + values.Encode()
}

func defaultCalendarCreateScheduledLocal(selectedDayLocal, nowLocal time.Time) string {
	if selectedDayLocal.IsZero() || nowLocal.IsZero() {
		return ""
	}
	loc := selectedDayLocal.Location()
	if loc == nil {
		loc = time.UTC
	}
	defaultTime := nowLocal.In(loc).Add(1 * time.Hour)
	return time.Date(
		selectedDayLocal.Year(),
		selectedDayLocal.Month(),
		selectedDayLocal.Day(),
		defaultTime.Hour(),
		defaultTime.Minute(),
		0,
		0,
		loc,
	).Format("2006-01-02T15:04")
}

func calendarSelectedDayFromCreateQuery(q url.Values, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.UTC
	}
	dayRaw := strings.TrimSpace(q.Get("calendar_day"))
	if dayRaw == "" {
		returnTo := strings.TrimSpace(q.Get("return_to"))
		if returnTo == "" {
			return time.Time{}, false
		}
		parsedReturnTo, err := url.Parse(returnTo)
		if err != nil {
			return time.Time{}, false
		}
		if strings.ToLower(strings.TrimSpace(parsedReturnTo.Query().Get("view"))) != "calendar" {
			return time.Time{}, false
		}
		dayRaw = strings.TrimSpace(parsedReturnTo.Query().Get("day"))
	}
	if dayRaw == "" {
		return time.Time{}, false
	}
	parsedDay, err := time.ParseInLocation("2006-01-02", dayRaw, loc)
	if err != nil {
		return time.Time{}, false
	}
	return parsedDay, true
}
