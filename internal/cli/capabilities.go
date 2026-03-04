package cli

import "github.com/antoniolg/postflow/internal/capabilities"

func ExposedCapabilities() map[string]struct{} {
	return map[string]struct{}{
		capabilities.CapabilityHealthCheck:          {},
		capabilities.CapabilityScheduleList:         {},
		capabilities.CapabilityDraftsList:           {},
		capabilities.CapabilityPostsCreate:          {},
		capabilities.CapabilityPostsSchedule:        {},
		capabilities.CapabilityPostsEdit:            {},
		capabilities.CapabilityPostsDelete:          {},
		capabilities.CapabilityPostsCancel:          {},
		capabilities.CapabilityPostsValidate:        {},
		capabilities.CapabilityAccountsList:         {},
		capabilities.CapabilityAccountsCreateStatic: {},
		capabilities.CapabilityAccountsConnect:      {},
		capabilities.CapabilityAccountsDisconnect:   {},
		capabilities.CapabilityAccountsDelete:       {},
		capabilities.CapabilityAccountsSetXPremium:  {},
		capabilities.CapabilityFailedList:           {},
		capabilities.CapabilityDLQRequeue:           {},
		capabilities.CapabilityDLQDelete:            {},
		capabilities.CapabilityMediaUpload:          {},
		capabilities.CapabilityMediaList:            {},
		capabilities.CapabilityMediaDelete:          {},
		capabilities.CapabilitySettingsTimezone:     {},
	}
}
