package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/db"
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
	oauthSelectID := strings.TrimSpace(r.URL.Query().Get("oauth_select"))
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
	publicationsWindowDays := 14
	publicationsFrom := nowLocal
	publicationsTo := nowLocal.AddDate(0, 0, publicationsWindowDays)
	publicationsRaw, err := s.Store.ListSchedule(r.Context(), publicationsFrom.UTC(), publicationsTo.UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for i := range publicationsRaw {
		if !publicationsRaw[i].ScheduledAt.IsZero() {
			publicationsRaw[i].ScheduledAt = publicationsRaw[i].ScheduledAt.In(uiLoc)
		}
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
	accountDisplayNames := make(map[string]string, len(accounts))
	for _, account := range accounts {
		accountDisplayNames[strings.TrimSpace(account.ID)] = strings.TrimSpace(account.DisplayName)
	}
	accountLabelFor := func(accountID string) string {
		return accountDisplayNames[strings.TrimSpace(accountID)]
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
	failedItems := make([]failedQueueItem, 0, len(deadLetters))
	failedEnvelopes := make([]failedPostEnvelope, 0, len(deadLetters))
	failedByPostID := make(map[string]failedPostEnvelope, len(deadLetters))
	noDateLabel := uiMessage(uiLang, "common.no_date")
	for _, dead := range deadLetters {
		post, err := s.Store.GetPost(r.Context(), dead.PostID)
		if err != nil {
			continue
		}
		if !post.ScheduledAt.IsZero() {
			post.ScheduledAt = post.ScheduledAt.In(uiLoc)
		}
		scheduledAtLabel := noDateLabel
		if !post.ScheduledAt.IsZero() {
			scheduledAtLabel = post.ScheduledAt.Format("2006-01-02 15:04")
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
		envelope := failedPostEnvelope{
			Post:         post,
			DeadLetterID: dead.ID,
			FailedAt:     dead.AttemptedAt,
			LastError:    dead.LastError,
		}
		failedEnvelopes = append(failedEnvelopes, envelope)
		failedByPostID[strings.TrimSpace(post.ID)] = envelope
	}

	firstWeekday := int(monthStartLocal.Weekday())
	firstWeekday = (firstWeekday + 6) % 7
	gridStart := monthStartLocal.AddDate(0, 0, -firstWeekday)

	lastDayLocal := monthEndLocal
	lastWeekday := int(lastDayLocal.Weekday())
	lastWeekday = (lastWeekday + 6) % 7
	gridEnd := lastDayLocal.AddDate(0, 0, 6-lastWeekday)

	items, err := s.Store.ListSchedule(r.Context(), gridStart.UTC(), gridEnd.UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for i := range items {
		if !items[i].ScheduledAt.IsZero() {
			items[i].ScheduledAt = items[i].ScheduledAt.In(uiLoc)
		}
	}

	rootIDs := make([]string, 0, len(items)+len(publicationsRaw)+len(drafts)+len(failedEnvelopes))
	rootIDs = append(rootIDs, orderedThreadRootsFromPosts(items)...)
	rootIDs = append(rootIDs, orderedThreadRootsFromPosts(publicationsRaw)...)
	rootIDs = append(rootIDs, orderedThreadRootsFromPosts(drafts)...)
	rootIDs = append(rootIDs, orderedThreadRootsFromFailedEnvelopes(failedEnvelopes)...)
	threadPostsByRoot := make(map[string][]domain.Post, len(rootIDs))
	for _, rootID := range rootIDs {
		rootID = strings.TrimSpace(rootID)
		if rootID == "" {
			continue
		}
		if _, exists := threadPostsByRoot[rootID]; exists {
			continue
		}
		threadPosts, err := s.Store.ListThreadPosts(r.Context(), rootID)
		if err != nil || len(threadPosts) == 0 {
			continue
		}
		for i := range threadPosts {
			if !threadPosts[i].ScheduledAt.IsZero() {
				threadPosts[i].ScheduledAt = threadPosts[i].ScheduledAt.In(uiLoc)
			}
		}
		threadPostsByRoot[rootID] = threadPosts
	}

	stepCountLabel := func(total int) string {
		if total <= 1 {
			return ""
		}
		return uiMessage(uiLang, "common.steps_count", total)
	}
	stepProgressLabel := func(done, total int) string {
		if total <= 1 || done <= 0 {
			return ""
		}
		return uiMessage(uiLang, "common.steps_progress", done, total)
	}

	publicationAllGroups := groupPublicationThreads(
		threadBundlesFromRoots(orderedThreadRootsFromPosts(publicationsRaw), threadPostsByRoot),
		failedByPostID,
		accountLabelFor,
		uiLoc,
		stepCountLabel,
		stepProgressLabel,
		noDateLabel,
	)
	publicationScheduledGroups := filterPublicationGroupsByStatus(publicationAllGroups, "scheduled")
	publicationPublishingGroups := filterPublicationGroupsByStatus(publicationAllGroups, "publishing")
	publicationGroups := append(append([]publicationGroupItem{}, publicationPublishingGroups...), publicationScheduledGroups...)

	draftGroups := filterPublicationGroupsByStatus(
		groupPublicationThreads(
			threadBundlesFromRoots(orderedThreadRootsFromPosts(drafts), threadPostsByRoot),
			nil,
			accountLabelFor,
			uiLoc,
			stepCountLabel,
			stepProgressLabel,
			noDateLabel,
		),
		"draft",
	)
	failedGroups := filterPublicationGroupsByStatus(
		groupPublicationThreads(
			threadBundlesFromRoots(orderedThreadRootsFromFailedEnvelopes(failedEnvelopes), threadPostsByRoot),
			failedByPostID,
			accountLabelFor,
			uiLoc,
			stepCountLabel,
			stepProgressLabel,
			noDateLabel,
		),
		"failed",
	)
	var nextRun *time.Time
	for _, group := range publicationGroups {
		if !group.ScheduledAt.IsZero() && (nextRun == nil || group.ScheduledAt.Before(*nextRun)) {
			t := group.ScheduledAt
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
			AccountKind: account.AccountKind,
			AccountMeta: settingsAccountMeta(uiLang, account),
			XPremium:    account.XPremium,
			AuthMethod:  account.AuthMethod,
			Status:      account.Status,
			StatusClass: statusClass,
			StatusLabel: statusLabel,
			LastError:   lastError,
		})
	}
	var oauthPendingSelection *oauthPendingSelectionView
	if view == "settings" && oauthSelectID != "" {
		payload, err := s.loadOAuthPendingSelection(r.Context(), oauthSelectID)
		if err != nil {
			if accountsError == "" {
				if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrOAuthPendingAccountSelectionExpired) {
					accountsError = uiMessage(uiLang, "settings.oauth_selection_expired")
				} else {
					accountsError = err.Error()
				}
			}
		} else {
			oauthPendingSelection = buildOAuthPendingSelectionView(uiLang, oauthSelectID, payload)
		}
	}

	calendarGroupsByDate := make(map[string][]publicationGroupItem)
	calendarGroups := groupPublicationThreads(
		threadBundlesFromRoots(orderedThreadRootsFromPosts(items), threadPostsByRoot),
		failedByPostID,
		accountLabelFor,
		uiLoc,
		stepCountLabel,
		stepProgressLabel,
		noDateLabel,
	)
	for _, group := range calendarGroups {
		if group.ScheduledAt.IsZero() {
			continue
		}
		key := group.ScheduledAt.In(uiLoc).Format("2006-01-02")
		calendarGroupsByDate[key] = append(calendarGroupsByDate[key], group)
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
		dayGroups := calendarGroupsByDate[key]
		dayEvents := groupCalendarEventsFromPublicationGroups(dayGroups)
		calendarDays = append(calendarDays, calendarDay{
			DateKey:        key,
			DayNumber:      d.Day(),
			InCurrentMonth: d.Month() == monthStartLocal.Month(),
			IsToday:        d.Year() == nowLocal.Year() && d.Month() == nowLocal.Month() && d.Day() == nowLocal.Day(),
			IsSelected:     d.Year() == selectedDayLocal.Year() && d.Month() == selectedDayLocal.Month() && d.Day() == selectedDayLocal.Day(),
			EventCount:     len(dayGroups),
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
	selectedDayGroups := calendarGroupsByDate[selectedDayKey]
	selectedDayPendingGroups := filterPublicationGroupsByStatus(selectedDayGroups, "scheduled")
	selectedDayPublishingGroups := filterPublicationGroupsByStatus(selectedDayGroups, "publishing")
	selectedDayPublishedGroups := filterPublicationGroupsByStatus(selectedDayGroups, "published")
	selectedDayFailedGroups := filterPublicationGroupsByStatus(selectedDayGroups, "failed")
	selectedDayPendingCount := len(selectedDayPendingGroups)
	selectedDayPublishingCount := len(selectedDayPublishingGroups)
	selectedDayPublishedCount := len(selectedDayPublishedGroups)
	selectedDayFailedCount := len(selectedDayFailedGroups)
	selectedDayOpenCount := selectedDayPendingCount + selectedDayPublishingCount
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
	for i := range publicationScheduledGroups {
		publicationScheduledGroups[i].EditURL = publicationGroupEditURL(publicationScheduledGroups[i], currentViewURL)
	}
	for i := range publicationPublishingGroups {
		publicationPublishingGroups[i].EditURL = publicationGroupEditURL(publicationPublishingGroups[i], currentViewURL)
	}
	for i := range draftGroups {
		draftGroups[i].EditURL = publicationGroupEditURL(draftGroups[i], currentViewURL)
	}
	for i := range failedGroups {
		failedGroups[i].EditURL = publicationGroupEditURL(failedGroups[i], currentViewURL)
	}
	for i := range selectedDayPendingGroups {
		selectedDayPendingGroups[i].EditURL = publicationGroupEditURL(selectedDayPendingGroups[i], currentViewURL)
	}
	for i := range selectedDayPublishingGroups {
		selectedDayPublishingGroups[i].EditURL = publicationGroupEditURL(selectedDayPublishingGroups[i], currentViewURL)
	}
	for i := range selectedDayFailedGroups {
		selectedDayFailedGroups[i].EditURL = publicationGroupEditURL(selectedDayFailedGroups[i], currentViewURL)
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
	editPostIDs := normalizeIDListCSV(r.URL.Query().Get("post_ids"))
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
		"ti": func(key string, args ...any) string {
			return uiMessageIndexed(uiLang, key, args...)
		},
		"inc": func(v int) int {
			return v + 1
		},
		"trim":      strings.TrimSpace,
		"hasPrefix": strings.HasPrefix,
		"accountSelected": func(accountID string) bool {
			_, ok := selectedCreateAccountIDs[strings.TrimSpace(accountID)]
			return ok
		},
		"accountOptionLabel": func(account domain.SocialAccount) string {
			parts := []string{strings.TrimSpace(account.DisplayName), strings.TrimSpace(string(account.Platform))}
			switch domain.NormalizeAccountKind(account.Platform, account.AccountKind) {
			case domain.AccountKindPersonal:
				parts = append(parts, uiMessage(uiLang, "settings.account_kind.personal"))
			case domain.AccountKindOrganization:
				parts = append(parts, uiMessage(uiLang, "settings.account_kind.organization"))
			}
			return strings.Join(parts, " · ")
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
		Lang:                        uiLang,
		View:                        view,
		ActiveNavView:               activeNavView,
		UITimezone:                  uiTimezone,
		TimezoneConfigured:          timezoneConfigured,
		AppVersion:                  appVersion,
		MCPURL:                      mcpURL,
		MCPAuthHint:                 mcpAuthHint,
		MCPConfigJSON:               mcpConfigJSON,
		MCPClaudeCommand:            mcpClaudeCommand,
		MCPCodexCommand:             mcpCodexCommand,
		MCPCodexConfigTOML:          mcpCodexConfigTOML,
		Items:                       items,
		Publications:                publicationsRaw,
		PublicationGroups:           publicationGroups,
		PublicationScheduledGroups:  publicationScheduledGroups,
		PublicationPublishingGroups: publicationPublishingGroups,
		Drafts:                      drafts,
		DraftGroups:                 draftGroups,
		FailedItems:                 failedItems,
		FailedGroups:                failedGroups,
		CurrentViewURL:              currentViewURL,
		CreateViewURL:               createViewURL,
		ReturnTo:                    returnTo,
		BackURL:                     backURL,
		Accounts:                    connectedAccounts,
		EditingPost:                 editingPost,
		CreateInitialMedia:          createInitialMedia,
		CreateInitialSegments:       createInitialSegments,
		CreateAccountID:             createAccountID,
		CreateAccountIDs:            createAccountIDs,
		EditPostIDs:                 editPostIDs,
		CreateText:                  createText,
		CreateScheduledLocal:        createScheduledLocal,
		CreateError:                 createError,
		CreateSuccess:               createSuccess,
		FailedError:                 failedError,
		FailedSuccess:               failedSuccess,
		SettingsError:               settingsError,
		SettingsSuccess:             settingsSuccess,
		AccountsError:               accountsError,
		AccountsSuccess:             accountsSuccess,
		OAuthPendingSelection:       oauthPendingSelection,
		MediaError:                  mediaError,
		MediaSuccess:                mediaSuccess,
		TotalAccountCount:           len(accounts),
		ConnectedAccountCount:       len(connectedAccounts),
		ScheduledCount:              len(publicationScheduledGroups) + len(publicationPublishingGroups),
		PublicationsWindowDays:      publicationsWindowDays,
		DraftCount:                  len(draftGroups),
		FailedCount:                 len(failedGroups),
		SettingsAccounts:            settingsAccounts,
		MediaLibrary:                mediaLibrary,
		CreateRecentMedia:           createRecentMedia,
		MediaInUseCount:             mediaInUseCount,
		MediaTotalSizeLabel:         mediaTotalSizeLabel,
		NextRunLabel:                nextRunLabel,
		CalendarMonthLabel:          calendarMonthLabel,
		WeekdayLabels:               weekdayLabels,
		CalendarWeeks:               calendarWeeks,
		PrevMonthParam:              prevMonthParam,
		NextMonthParam:              nextMonthParam,
		CurrentMonthParam:           currentMonthParam,
		TodayMonthParam:             todayMonthParam,
		TodayDayKey:                 todayDayKey,
		SelectedDayKey:              selectedDayKey,
		SelectedDayLabel:            selectedDayLabel,
		SelectedDayItems:            nil,
		SelectedDayPendingItems:     nil,
		SelectedDayPublishedItems:   nil,
		SelectedDayPendingGroups:    selectedDayPendingGroups,
		SelectedDayPublishingGroups: selectedDayPublishingGroups,
		SelectedDayPublishedGroups:  selectedDayPublishedGroups,
		SelectedDayFailedGroups:     selectedDayFailedGroups,
		SelectedDayPendingCount:     selectedDayPendingCount,
		SelectedDayPublishingCount:  selectedDayPublishingCount,
		SelectedDayPublishedCount:   selectedDayPublishedCount,
		SelectedDayFailedCount:      selectedDayFailedCount,
		SelectedDayOpenCount:        selectedDayOpenCount,
		DatePickerMonthNames:        datePickerMonthNames,
		DatePickerWeekdayNames:      datePickerWeekdayNames,
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
	return normalizeIDListCSV(raw)
}

func normalizeIDListCSV(raw string) []string {
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
	if len(group.EditPostIDs) > 0 {
		values.Set("post_ids", strings.Join(group.EditPostIDs, ","))
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

func settingsAccountMeta(uiLang string, account domain.SocialAccount) string {
	parts := []string{string(account.Platform)}
	switch domain.NormalizeAccountKind(account.Platform, account.AccountKind) {
	case domain.AccountKindPersonal:
		parts = append(parts, uiMessage(uiLang, "settings.account_kind.personal"))
	case domain.AccountKindOrganization:
		parts = append(parts, uiMessage(uiLang, "settings.account_kind.organization"))
	}
	parts = append(parts, string(account.AuthMethod))
	return strings.Join(parts, " · ")
}
