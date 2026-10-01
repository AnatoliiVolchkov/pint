package vmtest_test

import (
	"testing"
	"time"

	"github.com/cloudflare/pint/internal/promapi/vmtest"
)

func TestImportAndQuery(t *testing.T) {
	env := vmtest.Start(t)
	for _, tg := range env.Targets() {
		t.Run(tg.Name, func(t *testing.T) {
			name := "vmtest_fixture_" + env.RunID
			tg.Import(t, 15*time.Second,
				vmtest.Series{Metric: name + `{job="a"}`, Values: vmtest.Constant(20, 1)},
				vmtest.Series{Metric: name + `{job="b"}`, Values: vmtest.Gap(vmtest.Counter(20, 0, 10), 0, 10)},
			)
			res := tg.MustQuery(t, "sum("+name+")")
			if len(res) != 1 || res[0].Value != 1+190 {
				t.Fatalf("unexpected result: %v", res)
			}
			if _, err := tg.Query("sum("); err == nil {
				t.Fatal("expected an error for an invalid query")
			}
		})
	}
}
