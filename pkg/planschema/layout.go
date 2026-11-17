package planschema

// Container filesystem layout. Plans reference these absolute paths, so
// they are part of the schema contract.
const (
	WorkspaceDir = "/workspace"       // session-owned named volume
	SourceDir    = "/workspace/src"   // repository copy; default working dir
	ToolsDir     = "/workspace/tools" // npm --global prefix for pnpm/yarn
	VenvDir      = "/workspace/venv"  // Python virtual environment
	HomeDir      = "/workspace/home"  // $HOME for the unprivileged user
	CacheDir     = "/cache"           // shared, per-ecosystem package cache volume
	ContainerUID = 1000
)
