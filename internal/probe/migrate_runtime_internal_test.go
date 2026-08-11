package probe

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestMySQLRuntimeSchemaURLSelectsPreparedSchema(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		base string
		want string
	}{
		{
			name: "mysql",
			base: "mysql://root:pw@tcp(localhost:3306)/conf?parseTime=true",
			want: "mysql://root:pw@tcp(localhost:3306)/ptah_rt_mysql?parseTime=true",
		},
		{
			name: "mariadb",
			base: "mariadb://root:pw@tcp(localhost:3307)/conf",
			want: "mariadb://root:pw@tcp(localhost:3307)/ptah_rt_mariadb",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c := qt.New(t)
			got, err := mysqlRuntimeSchemaURL(test.base, "ptah_rt_"+test.name)
			c.Assert(err, qt.IsNil)
			c.Assert(got, qt.Equals, test.want)
		})
	}
}
