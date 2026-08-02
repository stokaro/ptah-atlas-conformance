package probe

// White-box testing required: these tests pin the exact stream, exit-code,
// and command-name contract used only by the internal Atlas CE sentinel gate.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestCompareCECommunityAbortContract_HappyPath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name    string
		command ptahCommandResult
		rules   CEGatingRules
	}{
		{
			name: "bare command owns stderr",
			command: ptahCommandResult{
				exitCode: 1,
				stderr:   "Abort: 'atlas schema plan' is not supported by the community version.\n",
			},
			rules: CEGatingRules{
				CommunityAbortPath:   "atlas schema plan",
				CommunityAbortExit:   1,
				CommunityAbortStream: "stderr",
			},
		},
		{
			name: "help command owns stdout",
			command: ptahCommandResult{
				stdout: "'atlas migrate push' is not supported by the community version.\ninstallation details\n",
			},
			rules: CEGatingRules{
				CommunityAbortPath:   "atlas migrate push",
				CommunityAbortStream: "stdout",
			},
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got := compareCECommunityAbortContract(test.command, test.rules)
			c.Assert(got, qt.Equals, "")
		})
	}
}

func TestCompareCECommunityAbortContract_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name    string
		command ptahCommandResult
		rules   CEGatingRules
		want    string
	}{
		{
			name: "missing process contract",
			command: ptahCommandResult{
				exitCode: 1,
				stderr:   "Abort: 'atlas schema push' is not supported by the community version.\n",
			},
			want: "community abort scenario has no process contract",
		},
		{
			name: "wrong exit code",
			command: ptahCommandResult{
				stderr: "Abort: 'atlas schema push' is not supported by the community version.\n",
			},
			rules: CEGatingRules{
				CommunityAbortPath:   "atlas schema push",
				CommunityAbortExit:   1,
				CommunityAbortStream: "stderr",
			},
			want: "community abort exited 0, want 1",
		},
		{
			name: "wrong stream",
			command: ptahCommandResult{
				exitCode: 1,
				stdout:   "'atlas schema push' is not supported by the community version.\n",
			},
			rules: CEGatingRules{
				CommunityAbortPath:   "atlas schema push",
				CommunityAbortExit:   1,
				CommunityAbortStream: "stderr",
			},
			want: "community abort wrote unexpected stdout: 'atlas schema push' is not supported by the community version.",
		},
		{
			name: "wrong command name",
			command: ptahCommandResult{
				exitCode: 1,
				stderr:   "Abort: 'atlas schema plan approve' is not supported by the community version.\n",
			},
			rules: CEGatingRules{
				CommunityAbortPath:   "atlas schema plan",
				CommunityAbortExit:   1,
				CommunityAbortStream: "stderr",
			},
			want: "community abort stderr did not name the measured CE abort path: Abort: 'atlas schema plan approve' is not supported by the community version.",
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got := compareCECommunityAbortContract(test.command, test.rules)
			c.Assert(got, qt.Equals, test.want)
		})
	}
}
