package api

import "github.com/antoniolg/publisher/internal/capabilities"

func HTTPExposedCapabilities() map[string]struct{} {
	return map[string]struct{}{
		capabilities.CapabilityScheduleList:  {},
		capabilities.CapabilityPostsCreate:   {},
		capabilities.CapabilityPostsValidate: {},
		capabilities.CapabilityFailedList:    {},
		capabilities.CapabilityDLQRequeue:    {},
		capabilities.CapabilityDLQDelete:     {},
		capabilities.CapabilityMediaUpload:   {},
		capabilities.CapabilityMediaList:     {},
		capabilities.CapabilityMediaDelete:   {},
	}
}

func MCPExposedCapabilities() map[string]struct{} {
	return map[string]struct{}{
		capabilities.CapabilityScheduleList:  {},
		capabilities.CapabilityPostsCreate:   {},
		capabilities.CapabilityPostsValidate: {},
		capabilities.CapabilityFailedList:    {},
		capabilities.CapabilityDLQRequeue:    {},
		capabilities.CapabilityDLQDelete:     {},
		capabilities.CapabilityMediaUpload:   {},
		capabilities.CapabilityMediaList:     {},
		capabilities.CapabilityMediaDelete:   {},
	}
}
