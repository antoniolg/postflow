package capabilities

type Surface string

const (
	SurfaceAPI Surface = "api"
	SurfaceMCP Surface = "mcp"
	SurfaceCLI Surface = "cli"
)

const (
	CapabilityScheduleList  = "schedule.list"
	CapabilityPostsCreate   = "posts.create"
	CapabilityPostsValidate = "posts.validate"
	CapabilityFailedList    = "failed.list"
	CapabilityDLQRequeue    = "dlq.requeue"
	CapabilityDLQDelete     = "dlq.delete"
	CapabilityMediaUpload   = "media.upload"
	CapabilityMediaList     = "media.list"
	CapabilityMediaDelete   = "media.delete"
)

type Capability struct {
	ID               string
	RequiredSurfaces []Surface
}

func RequiredParityCapabilities() []Capability {
	return []Capability{
		{ID: CapabilityScheduleList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsCreate, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityPostsValidate, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityFailedList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityDLQRequeue, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityDLQDelete, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityMediaUpload, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityMediaList, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
		{ID: CapabilityMediaDelete, RequiredSurfaces: []Surface{SurfaceAPI, SurfaceMCP, SurfaceCLI}},
	}
}
