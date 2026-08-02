package probe

type nonOSSSentinelPolicy string

const (
	nonOSSSentinelUnavailable nonOSSSentinelPolicy = "unavailable"
	nonOSSSentinelOpen        nonOSSSentinelPolicy = "open-extension"
)

// nonOSSSentinel separates three facts that must never be conflated: the path
// invoked against Atlas CE, the path named by CE's community abort (some plan
// children abort at their parent), and Ptah's own policy for that capability.
type nonOSSSentinel struct {
	invocationPath []string
	ceAbortPath    []string
	summary        string
	reason         string
	policy         nonOSSSentinelPolicy
}

func nonOSSSentinels() []nonOSSSentinel {
	return []nonOSSSentinel{
		{
			invocationPath: []string{"schema", "test"},
			ceAbortPath:    []string{"schema", "test"},
			summary:        "Test schemas through Atlas Cloud",
			reason:         "Atlas Pro/Cloud test workflow; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"schema", "plan"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Plan schema changes through Atlas Cloud",
			reason:         "Atlas Pro/Cloud plan workflow; Ptah implements the local plan-file half as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"schema", "plan", "approve"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Approve a plan in the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "lint"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Lint a plan against the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "list"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "List plans in the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "new"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Create a new schema plan file",
			reason:         "Atlas Pro plan workflow; Ptah implements the documented local file operation as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"schema", "plan", "pull"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Pull a plan from the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "push"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Push a plan to the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "rm"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Remove a plan from the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "test"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Test a plan through the Atlas Registry",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"schema", "plan", "validate"},
			ceAbortPath:    []string{"schema", "plan"},
			summary:        "Validate a schema plan file",
			reason:         "Atlas Pro plan workflow; Ptah implements the documented local validation as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"schema", "push"},
			ceAbortPath:    []string{"schema", "push"},
			summary:        "Push schema state to Atlas Cloud",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"migrate", "checkpoint"},
			ceAbortPath:    []string{"migrate", "checkpoint"},
			summary:        "Create migration checkpoint files",
			reason:         "Atlas Pro feature; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"migrate", "down"},
			ceAbortPath:    []string{"migrate", "down"},
			summary:        "Roll back migration files",
			reason:         "Atlas Pro rollback workflow; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"migrate", "edit"},
			ceAbortPath:    []string{"migrate", "edit"},
			summary:        "Edit migration files",
			reason:         "Atlas Pro directory-maintenance verb; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"migrate", "push"},
			ceAbortPath:    []string{"migrate", "push"},
			summary:        "Push migration directory to Atlas Cloud",
			reason:         "Atlas Cloud / registry workflow",
			policy:         nonOSSSentinelUnavailable,
		},
		{
			invocationPath: []string{"migrate", "rebase"},
			ceAbortPath:    []string{"migrate", "rebase"},
			summary:        "Rebase migration files",
			reason:         "Atlas Pro directory-maintenance verb; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"migrate", "rm"},
			ceAbortPath:    []string{"migrate", "rm"},
			summary:        "Remove migration files",
			reason:         "Atlas Pro directory-maintenance verb; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
		{
			invocationPath: []string{"migrate", "test"},
			ceAbortPath:    []string{"migrate", "test"},
			summary:        "Test migration files through Atlas Cloud",
			reason:         "Atlas Pro/Cloud test workflow; Ptah implements it as an open capability",
			policy:         nonOSSSentinelOpen,
		},
	}
}
