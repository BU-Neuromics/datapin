package figshare_test

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/contracttest"
	"github.com/BU-Neuromics/datapin/internal/backend/figshare"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakefigshare"
)

const testToken = "fig-token"

func TestContract_Figshare(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) backend.Backend {
		srv := fakefigshare.New(testToken)
		t.Cleanup(srv.Close)
		c, err := figshare.New(srv.URL(), testToken)
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}
