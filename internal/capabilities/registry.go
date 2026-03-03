package capabilities

type Surface string

const (
	SurfaceAPI Surface = "api"
	SurfaceMCP Surface = "mcp"
	SurfaceCLI Surface = "cli"
)

const (
	CapabilityHealthCheck          = "health.check"
	CapabilityScheduleList         = "schedule.list"
	CapabilityDraftsList           = "drafts.list"
	CapabilityPostsCreate          = "posts.create"
	CapabilityPostsSchedule        = "posts.schedule"
	CapabilityPostsEdit            = "posts.edit"
	CapabilityPostsDelete          = "posts.delete"
	CapabilityPostsCancel          = "posts.cancel"
	CapabilityPostsValidate        = "posts.validate"
	CapabilityAccountsList         = "accounts.list"
	CapabilityAccountsCreateStatic = "accounts.create_static"
	CapabilityAccountsConnect      = "accounts.connect"
	CapabilityAccountsDisconnect   = "accounts.disconnect"
	CapabilityAccountsDelete       = "accounts.delete"
	CapabilityAccountsSetXPremium  = "accounts.set_x_premium"
	CapabilityFailedList           = "failed.list"
	CapabilityDLQRequeue           = "dlq.requeue"
	CapabilityDLQDelete            = "dlq.delete"
	CapabilityMediaUpload          = "media.upload"
	CapabilityMediaList            = "media.list"
	CapabilityMediaDelete          = "media.delete"
	CapabilitySettingsTimezone     = "settings.timezone"
)

type Capability struct {
	ID               string
	RequiredSurfaces []Surface
}

func RequiredParityCapabilities() []Capability {
	return []Capability{
		{ID: CapabilityHealthCheck, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityScheduleList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityDraftsList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsCreate, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsSchedule, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsEdit, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsDelete, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsCancel, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsValidate, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityAccountsList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityAccountsCreateStatic, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityAccountsConnect, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityAccountsDisconnect, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityAccountsDelete, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityAccountsSetXPremium, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityFailedList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityDLQRequeue, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityDLQDelete, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityMediaUpload, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityMediaList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityMediaDelete, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilitySettingsTimezone, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
	}
}
