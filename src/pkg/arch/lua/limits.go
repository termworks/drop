package lua

// Process-wide limits for Lua archetype sessions and their host resources.
const (
	MaxSessions  = 16
	MaxFiles     = 128
	MaxProcesses = 4
)

type slots chan struct{}

func (s slots) take() bool {
	select {
	case s <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s slots) give() { <-s }

type resourceLimits struct {
	sessions  slots
	files     slots
	processes slots
}

func newResourceLimits(sessions, files, processes int) *resourceLimits {
	return &resourceLimits{
		sessions:  make(slots, sessions),
		files:     make(slots, files),
		processes: make(slots, processes),
	}
}

var processLimits = newResourceLimits(MaxSessions, MaxFiles, MaxProcesses)
