// White-box testing required: the module resolution and the go.mod it renders
// are unexported, and what they produce is a file inside a temporary directory
// that no exported result reports. The property under test -- a replacement in
// this repository's go.mod reaches the build -- is invisible from outside.
package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"
)

// TestParsePtahModuleList_ReadsTheReplacement pins that a replacement survives
// the decode.
//
// The version template this used to read answers only the version, so a
// replacement was invisible to the caller that had to honor it. Each row keeps
// the required version beside the replacement: a resolution that swapped the
// version for the replacement's would make a report name the wrong pin.
func TestParsePtahModuleList_ReadsTheReplacement(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    resolvedPtahModule
	}{
		{
			name:    "no replacement",
			payload: `{"Path":"ptah.run","Version":"v0.4.1"}`,
			want:    resolvedPtahModule{Version: "v0.4.1"},
		},
		{
			name: "a directory replacement",
			payload: `{"Path":"ptah.run","Version":"v0.4.1","Replace":` +
				`{"Path":"../ptah","Dir":"/home/dev/ptah"}}`,
			want: resolvedPtahModule{Version: "v0.4.1", ReplaceDir: "/home/dev/ptah"},
		},
		{
			name: "a module replacement",
			payload: `{"Path":"ptah.run","Version":"v0.4.1","Replace":` +
				`{"Path":"example.com/fork","Version":"v0.9.0"}}`,
			want: resolvedPtahModule{
				Version:        "v0.4.1",
				ReplacePath:    "example.com/fork",
				ReplaceVersion: "v0.9.0",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got, err := parsePtahModuleList([]byte(test.payload))

			c.Assert(err, qt.IsNil)
			c.Assert(got, qt.Equals, test.want)
		})
	}
}

// TestParsePtahModuleList_FailurePath keeps a payload naming no module from
// resolving to an empty version the build would then require literally.
func TestParsePtahModuleList_FailurePath(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{name: "not JSON", payload: "ptah.run v0.4.1", wantErr: "decoding the ptah.run module list: .*"},
		{name: "no version", payload: `{"Path":"ptah.run"}`, wantErr: "no ptah.run version in the build list"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got, err := parsePtahModuleList([]byte(test.payload))

			c.Assert(err, qt.ErrorMatches, test.wantErr)
			c.Assert(got, qt.Equals, resolvedPtahModule{})
		})
	}
}

// TestThrowawayGoMod_CarriesTheReplacement is the row this whole change exists
// for. A `replace ptah.run => ../ptah` in this repository is how a developer
// asks the tiers to measure a checkout; a build that dropped it would report
// the pin's behavior under the checkout's name.
//
// Every row asserts the require line too, so a rendering that honored the
// replacement by swapping the required version -- losing which pin the
// replacement stands in for -- fails here rather than in a report header.
func TestThrowawayGoMod_CarriesTheReplacement(t *testing.T) {
	tests := []struct {
		name     string
		resolved resolvedPtahModule
		want     string
	}{
		{
			name:     "no replacement writes no replace line",
			resolved: resolvedPtahModule{Version: "v0.4.1"},
			want:     "module ptahbuild\n\ngo 1.21\n\nrequire ptah.run v0.4.1\n",
		},
		{
			name:     "a directory replacement",
			resolved: resolvedPtahModule{Version: "v0.4.1", ReplaceDir: "/home/dev/ptah"},
			want: "module ptahbuild\n\ngo 1.21\n\nrequire ptah.run v0.4.1\n" +
				"\nreplace ptah.run => /home/dev/ptah\n",
		},
		{
			name: "a module replacement",
			resolved: resolvedPtahModule{
				Version:        "v0.4.1",
				ReplacePath:    "example.com/fork",
				ReplaceVersion: "v0.9.0",
			},
			want: "module ptahbuild\n\ngo 1.21\n\nrequire ptah.run v0.4.1\n" +
				"\nreplace ptah.run => example.com/fork v0.9.0\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(throwawayGoMod(test.resolved), qt.Equals, test.want)
		})
	}
}

// TestResolvedPtahModule_DescribeNamesWhatRan pins the sentence a report can
// use. A header that said only the pin while a checkout was built is the false
// green this change removes, so the description has to differ in that case.
func TestResolvedPtahModule_DescribeNamesWhatRan(t *testing.T) {
	tests := []struct {
		name     string
		resolved resolvedPtahModule
		want     string
	}{
		{
			name:     "the pin names itself",
			resolved: resolvedPtahModule{Version: "v0.4.1"},
			want:     "ptah.run v0.4.1",
		},
		{
			name:     "a directory replacement names the directory",
			resolved: resolvedPtahModule{Version: "v0.4.1", ReplaceDir: "/home/dev/ptah"},
			want:     "ptah.run v0.4.1 (replaced by /home/dev/ptah)",
		},
		{
			name: "a module replacement names the module",
			resolved: resolvedPtahModule{
				Version:        "v0.4.1",
				ReplacePath:    "example.com/fork",
				ReplaceVersion: "v0.9.0",
			},
			want: "ptah.run v0.4.1 (replaced by example.com/fork v0.9.0)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(test.resolved.Describe(), qt.Equals, test.want)
			c.Assert(test.resolved.Replaced(), qt.Equals, test.resolved.Describe() != "ptah.run "+test.resolved.Version)
		})
	}
}

// TestBuildPtahCommand_BuildsTheReplacement is the end-to-end half: the
// rendering tests above prove the go.mod says `replace`, and this proves the
// build acts on it.
//
// The replacement is a module declaring `module ptah.run` with a `cmd/ptah`
// that prints a marker, so the binary itself reports which source produced it.
// Comparing a path or reading the generated go.mod would leave the one step
// that matters -- go build honoring the directive -- unmeasured.
func TestBuildPtahCommand_BuildsTheReplacement(t *testing.T) {
	c := qt.New(t)
	fake := c.TempDir()
	c.Assert(os.WriteFile(filepath.Join(fake, "go.mod"),
		[]byte("module ptah.run\n\ngo 1.21\n"), 0o600), qt.IsNil)
	c.Assert(os.MkdirAll(filepath.Join(fake, "cmd", "ptah"), 0o750), qt.IsNil)
	c.Assert(os.WriteFile(filepath.Join(fake, "cmd", "ptah", "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"replacement\") }\n"), 0o600), qt.IsNil)

	// A module of our own that requires ptah.run and replaces it, standing in
	// for a developer's conformance checkout carrying the same directive.
	caller := c.TempDir()
	c.Assert(os.WriteFile(filepath.Join(caller, "go.mod"),
		[]byte("module callerprobe\n\ngo 1.21\n\nrequire ptah.run v0.4.1\n\nreplace ptah.run => "+fake+"\n"),
		0o600), qt.IsNil)
	t.Chdir(caller)

	resolved, err := resolvePtahModule()
	c.Assert(err, qt.IsNil)
	c.Assert(resolved.ReplaceDir, qt.Equals, fake)
	c.Assert(resolved.Replaced(), qt.IsTrue)

	bin, err := buildPtahCommand("ptah", "ptah.run/cmd/ptah")

	c.Assert(err, qt.IsNil)
	out, runErr := exec.Command(bin).Output()
	c.Assert(runErr, qt.IsNil)
	c.Assert(strings.TrimSpace(string(out)), qt.Equals, "replacement")
}
