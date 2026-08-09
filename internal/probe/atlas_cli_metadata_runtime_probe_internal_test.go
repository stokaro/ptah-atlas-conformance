package probe

// White-box testing required: the fixed checksum vector verifies the
// conformance harness's independent checksum oracle, which is intentionally
// not exported as product API.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestAtlasMetadataSingleFileSum(t *testing.T) {
	c := qt.New(t)

	got := atlasMetadataSingleFileSum(
		"20240101000000_init.sql",
		[]byte("-- +goose Up\n\n-- +goose Down\n"),
	)

	c.Assert(string(got), qt.Equals, "h1:+QWEU9n9l0FStqjJaFz03socAVwaCxLBFVxqPJPod3Y=\n"+
		"20240101000000_init.sql h1:Yv2XrGQxBw/obfLsFGOLPslmFCmNWNDOjvndAj1eBSo=\n")
}
